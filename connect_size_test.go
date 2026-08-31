package gologix

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

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

		var hdr eipHeader
		if err := binary.Read(conn, binary.LittleEndian, &hdr); err != nil {
			return
		}
		body := make([]byte, hdr.Length)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		// Echo back a successful RegisterSession reply, then close before the client's
		// next request (the forward-open) gets any response.
		resp := eipHeader{Command: hdr.Command, Length: hdr.Length, SessionHandle: 1, Context: hdr.Context}
		if err := binary.Write(conn, binary.LittleEndian, resp); err != nil {
			return
		}
		_, _ = conn.Write(body)
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
