package drawproof

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestNumericPoolMatchesStringPool(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	pool := make([]string, 0, 3000)
	var text strings.Builder
	for i := 0; i < 3000; i++ {
		n := r.Intn(2_000_000) + 1
		pool = append(pool, fmt.Sprint(n))
		text.WriteString(fmt.Sprintf("%d\n", n))
	}
	np := &NumericPool{}
	// Fed in two chunks split on a line boundary.
	all := text.String()
	cut := strings.Index(all[len(all)/2:], "\n") + len(all)/2 + 1
	if err := np.Feed(all[:cut]); err != nil {
		t.Fatal(err)
	}
	if err := np.Feed(all[cut:]); err != nil {
		t.Fatal(err)
	}
	if np.Len() != len(pool) {
		t.Fatalf("len %d want %d", np.Len(), len(pool))
	}
	if np.Digest() != DigestStringsSorted(pool) {
		t.Fatal("digest differs from DigestStringsSorted")
	}
	sorted := CanonicalOrder(pool)
	for i := range sorted {
		if np.At(i) != sorted[i] {
			t.Fatalf("At(%d) = %s want %s", i, np.At(i), sorted[i])
		}
	}
	if err := np.Feed("12x"); err == nil {
		t.Fatal("non-numeric entry accepted")
	}
}

func TestVerifyMainDrawIndexedMatchesVerifyMainDraw(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	pool := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		pool = append(pool, fmt.Sprint(r.Intn(100000)+1))
	}
	local, _ := GenerateSeed()
	beacon := []byte("beacon-value")
	final := MixExternalEntropy(local, beacon)
	winners := SelectWinners(CanonicalOrder(pool), 5, final)
	commit := CommitRecord{Kind: KindMainDraw, InputDigest: DigestStringsSorted(pool), SeedHash: HashSeed(local), BeaconPulse: 7, CommittedAt: "2026-01-01T00:00:00Z"}
	reveal := RevealRecord{Kind: KindMainDraw, Seed: fmt.Sprintf("%x", final), LocalSeed: fmt.Sprintf("%x", local), BeaconPulse: 7, BeaconValue: fmt.Sprintf("%x", beacon), Winners: winners, WinnerDigest: DigestStringsOrdered(winners)}

	want := VerifyMainDraw(pool, commit, reveal)
	if !want.OK {
		t.Fatalf("string verification should pass: %+v", want)
	}
	np := &NumericPool{}
	if err := np.Feed(strings.Join(pool, "\n")); err != nil {
		t.Fatal(err)
	}
	got := np.Verify(commit, reveal)
	if !got.OK || len(got.Checks) != len(want.Checks) {
		t.Fatalf("indexed verification differs: %+v vs %+v", got, want)
	}
	// A bundle whose pool is a file cannot be verified inline.
	if _, err := VerifyBundle(Bundle{Kind: KindMainDraw, PoolFile: "pool.txt", Commit: commit, Reveal: reveal}); err != ErrPoolNotLoaded {
		t.Fatalf("expected ErrPoolNotLoaded, got %v", err)
	}
	// Tampered winner fails.
	reveal.Winners[0] = "0"
	if np.Verify(commit, reveal).OK {
		t.Fatal("tampered winners verified")
	}
}
