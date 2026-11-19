package inputengine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"testing"
)

// starterFingerprint pins the exact lexicon the embedded starter data produces. It was
// recorded from the Go-constant version before the data moved into data/starter.json, so a
// non-matching value means the migration (or a later dictionary edit) lost or reordered
// characters. Change it only together with a deliberate dictionary change.
const starterFingerprint = "7dfb780988e196791aee48d72d5a6ac221d2c1aabe112abcf5e347b85b734c7d"

func lexiconFingerprint(l *lexicon) string {
	h := sha256.New()
	keys := make([]string, 0, len(l.bySyllable))
	for k := range l.bySyllable {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(h, "S|%s|", k)
		for _, r := range l.bySyllable[k] {
			fmt.Fprintf(h, "%c", r)
		}
		fmt.Fprint(h, "\n")
	}
	wkeys := make([]string, 0, len(l.words))
	for k := range l.words {
		wkeys = append(wkeys, k)
	}
	sort.Strings(wkeys)
	for _, k := range wkeys {
		fmt.Fprintf(h, "W|%s|%v\n", k, l.words[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestStarterDataMatchesRecordedFingerprint(t *testing.T) {
	if got := lexiconFingerprint(buildLexicon()); got != starterFingerprint {
		t.Fatalf("starter lexicon fingerprint = %s, want %s — the embedded data changed", got, starterFingerprint)
	}
}

// The embedded artifact must carry a version and a digest that matches its own bytes; a
// silently corrupted starter would break every lookup.
func TestStarterDataDigestAndShape(t *testing.T) {
	sum := sha256.Sum256(starterJSON)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != starterDigest {
		t.Fatalf("embedded digest = %s, want %s", got, starterDigest)
	}
	d := loadStarter()
	if d.Version < 1 {
		t.Fatalf("starter version = %d", d.Version)
	}
	if n := len([]rune(d.Hanzi)); n < 1000 {
		t.Fatalf("starter has %d hanzi, expected the frequency list", n)
	}
	if len(d.Words) < 100 {
		t.Fatalf("starter has %d words, expected the phrase list", len(d.Words))
	}
}
