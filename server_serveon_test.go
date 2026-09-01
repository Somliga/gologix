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
	go func() { _ = srv.ServeOn(l, p) }()

	// The server must be reachable on the address WE chose, not on 44818.
	c, err := net.DialTimeout("tcp", l.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial the listener we supplied: %v", err)
	}
	_ = c.Close()

	if srv.TCPListener != l {
		t.Errorf("srv.TCPListener = %v, want the listener passed in — ServeOn must not open its own",
			srv.TCPListener)
	}
	if srv.UDPListener != p {
		t.Errorf("srv.UDPListener = %v, want the conn passed in", srv.UDPListener)
	}
}
