package gologix

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
)

// TestCountIOIsThatFitUsesActiveSizeNotConfigured pins fix round 1 of fork patch 3:
// countIOIsThatFit (and readList's inline packer, same field) must pack against the size the
// wire actually negotiated, not the caller's configured ConnectionSize. Before this fix both
// read client.ConnectionSize directly, which was only correct by accident because Connect used
// to mutate that field down on a fallback — the very mutation this patch stops. Two directions:
// a negotiated size below the configured one must shrink what fits, and a client that has never
// negotiated (negotiatedSize == 0) must still use the configured value.
func TestCountIOIsThatFitUsesActiveSizeNotConfigured(t *testing.T) {
	const total = 60
	tags := make([]tagDesc, total)
	for i := range tags {
		tags[i] = tagDesc{TagName: fmt.Sprintf("tg%04d", i), TagType: CIPTypeDINT, Elements: 1}
	}
	discard := &Logger{internalLogger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// ConnectionSize 4000, but the wire only negotiated 511 (a fallback happened) — must pack
	// against 511, so not everything fits in one pass.
	small := &Client{ConnectionSize: connSizeLargeDefault, negotiatedSize: connSizeStandardDefault, Logger: discard}
	nSmall, err := small.countIOIsThatFit(tags)
	if err != nil {
		t.Fatalf("countIOIsThatFit (negotiated 511): %v", err)
	}
	if nSmall >= total {
		t.Fatalf("negotiatedSize=511 but all %d tags fit in one pass (n=%d) — fixture doesn't "+
			"exercise the small path, add more tags", total, nSmall)
	}

	// Never negotiated (fresh client): must fall back to the configured ConnectionSize, 4000 —
	// everything fits in one pass.
	large := &Client{ConnectionSize: connSizeLargeDefault, negotiatedSize: 0, Logger: discard}
	nLarge, err := large.countIOIsThatFit(tags)
	if err != nil {
		t.Fatalf("countIOIsThatFit (never negotiated): %v", err)
	}
	if nLarge != total {
		t.Fatalf("negotiatedSize=0 should fall back to ConnectionSize=%d, fitting all %d tags; got n=%d",
			connSizeLargeDefault, total, nLarge)
	}
}
