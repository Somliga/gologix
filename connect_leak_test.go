package gologix

import (
	"net"
	"os"
	"testing"
	"time"
)

// TestConnectClosesItsSocketOnFailure pins the fix for a leak that only fires when TCP
// completes and CIP does not. Connect assigns client.conn from net.DialTimeout, and every
// failure after that point returns without closing it — because the force-disconnect that
// protects every other path is refused while connStatus is connectionStatusConnecting.
func TestConnectClosesItsSocketOnFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close() // accept, then refuse CIP
		}
	}()

	count := func() int {
		ents, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Skip("no /proc/self/fd on this host")
		}
		return len(ents)
	}

	addr := ln.Addr().(*net.TCPAddr)
	mk := func() *Client {
		c := NewClient("127.0.0.1")
		c.Controller.Port = uint(addr.Port)
		c.SocketTimeout = 500 * time.Millisecond
		c.AutoConnect = false
		return c
	}
	_ = mk().Connect() // warm up

	before := count()
	for i := 0; i < 20; i++ {
		if err := mk().Connect(); err == nil {
			t.Fatal("Connect succeeded against a listener that refuses CIP")
		}
	}
	if delta := count() - before; delta > 2 {
		t.Errorf("leaked %d sockets across 20 failed connects", delta)
	}
}
