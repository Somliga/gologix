package gologix

import (
	"net"
	"testing"
	"time"
)

// TestServeOnUsesTheGivenListeners pins fork patch 2. Serve() hard-codes 0.0.0.0:44818, which is
// one server per machine, no parallel tests, and a fake controller on every interface — including
// a plant subnet reachable over a VPN. A caller must be able to supply its own listeners, and the
// one it supplies must be the one served.
func TestServeOnUsesTheGivenListeners(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	srv := NewServer(nil)
	done := make(chan error, 1)
	go func() { done <- srv.ServeOn(l, p) }()

	// The server must be reachable on the address WE chose, not on 44818.
	c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial the listener we supplied: %v", err)
	}
	_ = c.Close()

	// Read the fields only after ServeOn has returned. ServeOn writes them from its own goroutine
	// (server.go:86-87), so reading them while it runs is a data race that -race reports. The
	// receive below is the happens-before edge that makes these reads legal.
	_ = l.Close()
	<-done

	if srv.TCPListener != l {
		t.Errorf("srv.TCPListener = %v, want the listener passed in — ServeOn must not open its own",
			srv.TCPListener)
	}
	if srv.UDPListener != p {
		t.Errorf("srv.UDPListener = %v, want the conn passed in", srv.UDPListener)
	}
}

// TestServeTCPExitsWhenTheListenerCloses pins fork patch 4. The accept loop treated every error
// as transient and continued, but a closed listener returns net.ErrClosed immediately and forever,
// so the loop spins at ~895,000 iterations/second. A stopped server must stop, not burn a core.
func TestServeTCPExitsWhenTheListenerCloses(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	srv := NewServer(nil)
	done := make(chan error, 1)
	go func() { done <- srv.ServeOn(l, p) }()

	// Give it a moment to reach Accept, then take the listener away.
	time.Sleep(50 * time.Millisecond)
	_ = l.Close()

	select {
	case <-done:
		// Returned, which is the point.
	case <-time.After(2 * time.Second):
		t.Fatal("ServeOn did not return within 2s of its listener closing — the accept loop is " +
			"treating net.ErrClosed as transient and spinning")
	}
}

// TestServeUDPExitsWhenTheConnCloses is the UDP twin, and it exists because the UDP side had the
// same spin by a DIFFERENT mechanism: serveUDP checked `n == 0` before `err`, and a closed
// PacketConn's ReadFrom returns net.ErrClosed WITH n == 0, so it took the "read 0 bytes" branch and
// never reached the error check. Symmetry of the fix is not evidence of the fix.
//
// ServeOn returns when either goroutine errors, so closing only the PacketConn is enough to prove
// the UDP loop exits on its own — the TCP listener is left open deliberately.
func TestServeUDPExitsWhenTheConnCloses(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(nil)
	done := make(chan error, 1)
	go func() { done <- srv.ServeOn(l, p) }()

	// Give it a moment to reach ReadFrom, then take the conn away.
	time.Sleep(50 * time.Millisecond)
	_ = p.Close()

	select {
	case <-done:
		// Returned, which is the point.
	case <-time.After(2 * time.Second):
		t.Fatal("ServeOn did not return within 2s of its PacketConn closing — serveUDP is " +
			"treating net.ErrClosed as a short read and spinning")
	}
}
