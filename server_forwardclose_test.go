package gologix

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// TestDisconnectIsPromptWhenForwardCloseIsAnswered pins fork patch 5. The server parsed
// ForwardClose and replied to nothing, so every client Disconnect waited out its full
// SocketTimeout — 10 s at gologix's default, per teardown. A request/response service that never
// responds is the whole defect.
func TestDisconnectIsPromptWhenForwardCloseIsAnswered(t *testing.T) {
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

	host, port, _ := net.SplitHostPort(l.Addr().String())
	c := NewClient(host)
	c.AutoConnect = false
	c.SocketTimeout = 5 * time.Second
	if n, err := strconv.Atoi(port); err == nil {
		c.Controller.Port = uint(n)
	}
	if err := c.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}

	start := time.Now()
	_ = c.Disconnect()
	if d := time.Since(start); d > time.Second {
		t.Errorf("Disconnect took %v, want well under 1s — the server is not answering "+
			"ForwardClose, so the client is waiting out SocketTimeout", d)
	}
}
