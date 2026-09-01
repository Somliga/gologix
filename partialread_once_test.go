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

// TestCountThatFitRefusesWhenNothingFits covers the fix for an upstream quirk that was worse
// than it looked. countIOIsThatFit starts n at 1 — n doubles as the jump-table reservation for
// the IOI under test — and raised it only after an IOI was packed, so when even the first tag
// overflowed it still answered 1.
//
// At len(tags) >= 2 that was merely a bad number: ReadListOnce's `fit < len(tags)` guard still
// refused. At len(tags) == 1 the guard evaluates false, ReadListOnce proceeds, and readList's
// split branch computes tags[:0] and tags[0:] — the second being the SAME slice — so readList
// recurses on its own argument for as long as the device tolerates the empty Multiple Service
// Packet each level sends first. Unbounded requests as fast as the socket allows, from inside a
// single call, underneath whatever layer the caller does its pacing in.
//
// The floor also made a consumer's guard dead code: surtr's poll.go checks `fit <= 0`, which a
// floor of 1 can never satisfy. Refusing here is what makes that guard live.
//
// Both list lengths are asserted because they fail for different reasons, and only the second
// could burst a device.
func TestCountThatFitRefusesWhenNothingFits(t *testing.T) {
	for _, n := range []int{3, 1} {
		tagnames, types, elements := listOf(n)

		got, err := quietClient(1).CountThatFit(tagnames, types, elements)
		if err == nil {
			t.Errorf("CountThatFit(%d tags at ConnectionSize 1) = %d, nil; want a refusal — a tag "+
				"that does not fit alone cannot be made to fit by splitting, and answering 1 sends "+
				"readList into unbounded self-recursion at len(tags)==1", n, got)
			continue
		}
		if !strings.Contains(err.Error(), "does not fit") {
			t.Errorf("CountThatFit(%d tags) error = %q; want it to name the cause", n, err)
		}
	}
}

// TestCountThatFitStillCountsWhenSomethingFits is the other half of the above, and exists because
// a countIOIsThatFit that returned an error unconditionally would pass that test alone.
func TestCountThatFitStillCountsWhenSomethingFits(t *testing.T) {
	tagnames, types, elements := listOf(3)

	n, err := quietClient(4000).CountThatFit(tagnames, types, elements)
	if err != nil {
		t.Fatalf("CountThatFit at 4000: %v", err)
	}
	if n != 3 {
		t.Errorf("CountThatFit at 4000 = %d, want all 3 — the nothing-fits refusal must not fire "+
			"when the tags do fit", n)
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
