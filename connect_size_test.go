package gologix

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// readFrame reads one EIP request off conn: the fixed header, then its declared-length body.
func readFrame(conn net.Conn) (eipHeader, []byte, bool) {
	var hdr eipHeader
	if err := binary.Read(conn, binary.LittleEndian, &hdr); err != nil {
		return hdr, nil, false
	}
	body := make([]byte, hdr.Length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return hdr, nil, false
	}
	return hdr, body, true
}

// writeFrame replies on the same session/context as req, with body as the payload.
func writeFrame(conn net.Conn, req eipHeader, body []byte) bool {
	resp := eipHeader{Command: req.Command, Length: uint16(len(body)), SessionHandle: 1, Context: req.Context}
	if err := binary.Write(conn, binary.LittleEndian, resp); err != nil {
		return false
	}
	_, err := conn.Write(body)
	return err == nil
}

// TestFailedConnectDoesNotShrinkConnectionSize pins fork patch 3. connect.go lowers
// ConnectionSize from connSizeLargeDefault (4000) to connSizeStandardDefault (511) when a
// large forward-open fails, and leaves it there for the life of the client. That is not
// cosmetic downstream: the adapter derives its request count from this number, and at 511
// twelve tags need two messages where at 4000 they need one.
func TestFailedConnectDoesNotShrinkConnectionSize(t *testing.T) {
	// A dial failure returns from Connect before the forward-open block is ever reached, so
	// a listener that just refuses the TCP connection proves nothing here. This one accepts,
	// answers RegisterSession successfully (so Connect gets past that step), then closes —
	// which fails the *large forward-open* specifically, the block this patch changes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Echo back a successful RegisterSession reply, then close before the client's
		// next request (the forward-open) gets any response.
		hdr, body, ok := readFrame(conn)
		if !ok {
			return
		}
		writeFrame(conn, hdr, body)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	c := NewClient("127.0.0.1")
	c.Controller.Port = uint(addr.Port)
	c.SocketTimeout = 500 * time.Millisecond
	c.AutoConnect = false
	if c.ConnectionSize != connSizeLargeDefault {
		t.Fatalf("precondition: ConnectionSize = %d, want %d", c.ConnectionSize, connSizeLargeDefault)
	}
	if err := c.Connect(); err == nil {
		t.Fatal("Connect succeeded against a listener that refuses the forward-open")
	}
	if c.ConnectionSize != connSizeLargeDefault {
		t.Errorf("ConnectionSize = %d after a failed connect; want it unchanged at %d",
			c.ConnectionSize, connSizeLargeDefault)
	}
}

// TestFallbackSucceedsReportsNegotiatedSize pins the path the failure test above cannot reach:
// a large forward-open that is gracefully REJECTED at the CIP level (a well-behaved device that
// doesn't support it — the connection stays open, unlike the dropped-connection failure above),
// followed by a standard forward-open that succeeds. This is the path a real CompactLogix
// without large-forward-open support takes. NegotiatedSize() must report what was actually
// agreed (511) while ConnectionSize stays at the caller's configured value (4000) — that gap is
// the entire point of this patch.
func TestFallbackSucceedsReportsNegotiatedSize(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// 1. RegisterSession: succeed.
		hdr, body, ok := readFrame(conn)
		if !ok {
			return
		}
		if !writeFrame(conn, hdr, body) {
			return
		}

		// 2. Large forward-open: a graceful CIP-level rejection (bad status in an otherwise
		// well-formed reply), not a dropped connection — that failure mode is already covered
		// by TestFailedConnectDoesNotShrinkConnectionSize above.
		hdr, _, ok = readFrame(conn)
		if !ok {
			return
		}
		rejectItems := []CIPItem{
			newItem(cipItem_Null, nil),
			newItem(cipItem_UnconnectedData, nil),
		}
		if err := rejectItems[1].Serialize(msgUnconnWriteResultHeader{
			Service: CIPService_LargeForwardOpen.AsResponse(),
			Status:  0x08, // service not supported
		}); err != nil {
			return
		}
		rejectData, err := serializeItems(rejectItems)
		if err != nil {
			return
		}
		if !writeFrame(conn, hdr, *rejectData) {
			return
		}

		// 3. Standard forward-open: succeed, using the same reply shape server.go's own
		// forwardOpen() sends — proven against this same client by TestConnectedReplyUsesTOConnID.
		hdr, _, ok = readFrame(conn)
		if !ok {
			return
		}
		okItems := []CIPItem{
			newItem(cipItem_Null, nil),
			newItem(cipItem_UnconnectedData, nil),
		}
		if err := okItems[1].Serialize(msgEIPForwardOpen_Standard_Reply{
			Service:        CIPService_ForwardOpen.AsResponse(),
			OTConnectionID: 1,
			TOConnectionID: 2,
		}); err != nil {
			return
		}
		okData, err := serializeItems(okItems)
		if err != nil {
			return
		}
		writeFrame(conn, hdr, *okData)
	}()

	addr := ln.Addr().(*net.TCPAddr)
	c := NewClient("127.0.0.1")
	c.Controller.Port = uint(addr.Port)
	c.SocketTimeout = 500 * time.Millisecond
	c.AutoConnect = false

	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got, want := c.NegotiatedSize(), uint16(connSizeStandardDefault); got != want {
		t.Errorf("NegotiatedSize() = %d, want %d", got, want)
	}
	if c.ConnectionSize != connSizeLargeDefault {
		t.Errorf("ConnectionSize = %d after a successful fallback; want it unchanged at %d",
			c.ConnectionSize, connSizeLargeDefault)
	}
}
