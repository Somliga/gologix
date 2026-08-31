package gologix

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// quietClient never connects. countIOIsThatFit is pure arithmetic over ConnectionSize and
// the IOI encoder — firmware() falls through to 0 without a dial when no address is set —
// so the fitting behaviour is testable with no wire. Logger must be non-nil: countIOIsThatFit
// ends in a Debug call.
func quietClient(size uint16) *Client {
	return &Client{
		ConnectionSize: size,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// listOf builds n tags whose names are long enough that a small ConnectionSize must split
// them, which is the condition under test.
func listOf(n int) ([]string, []any, []int) {
	tagnames := make([]string, n)
	types := make([]any, n)
	elements := make([]int, n)
	for i := range tagnames {
		tagnames[i] = "Program:MainProgram." + strings.Repeat("x", 40) + string(rune('a'+i%26))
		types[i] = float32(0)
		elements[i] = 1
	}
	return tagnames, types, elements
}

// TestCountThatFitSplitsOnConnectionSize pins fork patch 7's arithmetic. ReadList loops
// internally — countIOIsThatFit, send, repeat — so an oversized list silently becomes
// several requests back to back inside one call, where a caller's burst limiter cannot see
// or pace them. The adapter derives its per-cycle request count from this number, so a wrong
// answer here becomes a rule 4 pacing error on a live plant. Asserted in BOTH directions: an
// assertion that only checks the list fits passes a function that always returns len(tags).
func TestCountThatFitSplitsOnConnectionSize(t *testing.T) {
	tagnames, types, elements := listOf(11)

	big, err := quietClient(4000).CountThatFit(tagnames, types, elements)
	if err != nil {
		t.Fatalf("CountThatFit at 4000: %v", err)
	}
	if big != 11 {
		t.Errorf("at ConnectionSize 4000 all 11 tags should fit one message, got %d", big)
	}

	small, err := quietClient(200).CountThatFit(tagnames, types, elements)
	if err != nil {
		t.Fatalf("CountThatFit at 200: %v", err)
	}
	if small >= 11 {
		t.Errorf("at ConnectionSize 200 the list must NOT fit one message, got %d", small)
	}
}

// TestCountThatFitReportsOneWhenNothingFits documents an upstream quirk rather than
// endorsing it. countIOIsThatFit starts n at 1 and raises it only after an IOI is packed, so
// when even the first tag overflows it still answers 1. ReadList is unharmed — it makes
// progress either way — but a caller pacing on this number would plan one request for a read
// that cannot be sent. Pinned so a later fork rebase cannot change it silently.
func TestCountThatFitReportsOneWhenNothingFits(t *testing.T) {
	tagnames, types, elements := listOf(3)

	n, err := quietClient(1).CountThatFit(tagnames, types, elements)
	if err != nil {
		t.Fatalf("CountThatFit at 1: %v", err)
	}
	if n != 1 {
		t.Errorf("upstream quirk changed: expected the floor of 1, got %d", n)
	}
}

func TestCountThatFitRejectsMismatchedLengths(t *testing.T) {
	_, err := quietClient(4000).CountThatFit([]string{"a", "b"}, []any{float32(0)}, []int{1, 1})
	if err == nil {
		t.Fatal("mismatched slice lengths must be refused, not silently truncated")
	}
}

// TestReadListOnceNeedsAConnection covers the guard that runs before any arithmetic.
// ReadListOnce's refusal-to-split branch needs a live session to reach, and is covered
// against the simulator in Plan 2.
func TestReadListOnceNeedsAConnection(t *testing.T) {
	_, err := quietClient(4000).ReadListOnce(listOf(3))
	if err == nil {
		t.Fatal("ReadListOnce on an unconnected client must fail")
	}
}
