package gologix

import (
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestServeOnLeaksNoGoroutine pins the buffering of ServeOn's error channel.
//
// This is a regression introduced by patch af5ecef, not by upstream. Before it, neither accept
// loop ever returned, so neither ever sent on errCh and an unbuffered channel was harmless.
// Making the loops exit on a closed listener made BOTH of them send, while ServeOn still reads
// exactly one value — so the second sender blocks on the send forever. af5ecef traded a pegged
// core for a leaked goroutine per server lifecycle, which is the quieter of the two bugs and
// therefore the one that would have shipped.
//
// It matters to a consumer: surtr's internal/absim starts a server per test, so an unbuffered
// channel there is one leaked goroutine per test, under a harness whose leak check is one of its
// selling points.
//
// Several lifecycles rather than one, because a single leak is within the noise of the runtime's
// own bookkeeping; a delta that scales with the loop count is not.
func TestServeOnLeaksNoGoroutine(t *testing.T) {
	const lifecycles = 5

	settle := func() {
		for i := 0; i < 5; i++ {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
		}
	}

	settle()
	before := runtime.NumGoroutine()

	for i := 0; i < lifecycles; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		srv := NewServer(nil)
		done := make(chan error, 1)
		go func() { done <- srv.ServeOn(l, p) }()

		// Let both accept loops reach Accept before tearing down, so both of them return an error
		// and both try to send. Closing before they start would leave the second send unattempted
		// and the test would pass with an unbuffered channel.
		time.Sleep(30 * time.Millisecond)

		_ = l.Close()
		_ = p.Close()
		<-done
	}

	settle()
	after := runtime.NumGoroutine()

	if delta := after - before; delta >= lifecycles {
		buf := make([]byte, 1<<16)
		buf = buf[:runtime.Stack(buf, true)]
		blocked := strings.Count(string(buf), "chan send")
		t.Errorf("goroutines %d -> %d (delta %d) over %d server lifecycles, and %d goroutine(s) "+
			"are parked on a channel send: ServeOn's errCh must be buffered for both accept "+
			"loops, since each returns and each sends but only one value is ever read",
			before, after, delta, lifecycles, blocked)
	}
}
