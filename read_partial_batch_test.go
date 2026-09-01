package gologix

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// oneBadTag serves every tag but one, which it refuses the way a controller refuses a renamed
// or deleted tag. Read-only on purpose: this fixture must not be able to create the tag it is
// asked about, which is exactly what MapTagProvider would do on a write.
type oneBadTag struct{ bad string }

func (p oneBadTag) TagRead(tag string, qty int16) (any, error) {
	if strings.EqualFold(tag, p.bad) {
		return nil, fmt.Errorf("tag %q does not exist on this controller", tag)
	}
	return float32(1.5), nil
}
func (p oneBadTag) TagWrite(string, any) error { return errors.New("read-only fixture") }
func (p oneBadTag) IORead() ([]byte, error)    { return nil, errors.New("read-only fixture") }
func (p oneBadTag) IOWrite([]CIPItem) error    { return errors.New("read-only fixture") }

// serveOneBadTag boots the server in-process on loopback and returns a connected client.
func serveOneBadTag(t *testing.T, bad string) *Client {
	t.Helper()

	path, err := ParsePath("1,0")
	if err != nil {
		t.Fatalf("parse path: %v", err)
	}
	var router PathRouter
	router.Handle(path.Bytes(), oneBadTag{bad: bad})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	srv := NewServer(&router)
	go func() { _ = srv.ServeOn(ln, pc) }()
	t.Cleanup(func() {
		_ = ln.Close()
		_ = pc.Close()
	})

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split %q: %v", ln.Addr(), err)
	}
	client := NewClient(host)
	client.AutoConnect = false
	client.SocketTimeout = 2 * time.Second
	var p uint
	if _, err := fmt.Sscan(port, &p); err != nil {
		t.Fatalf("parse port %q: %v", port, err)
	}
	client.Controller.Port = p
	if err := client.Connect(); err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect() })
	return client
}

// TestOneUnreadableTagKeepsTheRestOfTheBatch is the round trip both halves of the partial-read
// fix exist for, and it goes over a real socket because that is where the old behaviour was
// invisible: the server returned an error from connectedMulti, which unwound to NO REPLY and a
// dropped TCP connection, and the client then discarded every value in the batch. One renamed
// tag among eleven cost all eleven readings, every cycle, indefinitely.
//
// Every position is exercised, and the LAST one is not redundant: a failing service's reply is
// four bytes, so parsing it with the six-byte success layout runs off the end of the packet only
// when nothing follows it. In any other position it would quietly read the NEXT service's bytes
// as its own type header and go unnoticed.
//
// MUTATION: restore `return fmt.Errorf(...)` in connectedMulti's TagRead failure branch — this
// fails with "forced disconnect: problem reading header from socket: EOF", because there is no
// reply at all. Restore read.go's per-service `return nil, ...` and it fails with "problem
// reading {gone_tag ...}. Status 5" instead of a *PartialReadError.
func TestOneUnreadableTagKeepsTheRestOfTheBatch(t *testing.T) {
	for _, badIdx := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("failing_service_at_%d", badIdx), func(t *testing.T) {
			oneUnreadableTag(t, badIdx)
		})
	}
}

func oneUnreadableTag(t *testing.T, badIdx int) {
	const bad = "gone_tag"
	client := serveOneBadTag(t, bad)

	names := []string{"good_a", "good_b", "good_c"}
	names[badIdx] = bad
	types := []any{float32(0), float32(0), float32(0)}
	elems := []int{1, 1, 1}

	vals, err := client.ReadList(names, types, elems)
	if err == nil {
		t.Fatal("ReadList over a batch containing an unknown tag returned no error; the caller " +
			"would not know one reading is missing")
	}
	var partial *PartialReadError
	if !errors.As(err, &partial) {
		t.Fatalf("ReadList error = %v (%T); want a *PartialReadError naming the failing tag", err, err)
	}
	if len(partial.Failed) != 1 {
		t.Fatalf("PartialReadError names %d failures, want exactly 1: %v", len(partial.Failed), partial.Failed)
	}
	if got := partial.Failed[0]; got.Index != badIdx || !strings.EqualFold(got.Tag, bad) {
		t.Errorf("failure = index %d tag %q, want index %d tag %q", got.Index, got.Tag, badIdx, bad)
	}
	if got := partial.Failed[0].Status; got != CIPStatus_PathDestinationUnknown {
		t.Errorf("failing service status = %v (0x%02X), want PathDestinationUnknown (0x05) — the "+
			"status a controller gives an unknown tag", got, byte(got))
	}

	// The values that DID come back must still be there, in position.
	if len(vals) != len(names) {
		t.Fatalf("got %d values for %d tags; a partial failure must keep every tag's position", len(vals), len(names))
	}
	for i := range names {
		var want any = float32(1.5)
		if i == badIdx {
			want = nil
		}
		if vals[i] != want {
			t.Errorf("vals[%d] = %#v, want %#v — a failed read must be nil, never a zero value", i, vals[i], want)
		}
	}

	// The connection must have survived. This is the half no test could reach before: the old
	// server dropped the socket, so a second cycle had to redial.
	if !client.Connected() {
		t.Fatal("client is no longer connected after a partial read — the server hung up")
	}
	again, err := client.ReadList([]string{"good_a"}, []any{float32(0)}, []int{1})
	if err != nil {
		t.Fatalf("second read on the same session: %v — the session did not survive the partial failure", err)
	}
	if len(again) != 1 || again[0] != float32(1.5) {
		t.Fatalf("second read returned %#v, want [1.5]", again)
	}
}
