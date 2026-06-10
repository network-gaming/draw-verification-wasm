package drawproof

import (
	"encoding/hex"
	"strconv"
	"testing"
)

func TestVerifyBundle_Dispatch(t *testing.T) {
	// MAIN_DRAW bundle.
	p := pool(200)
	local := []byte("bundle-local")
	bv := []byte("bundle-beacon")
	final := MixExternalEntropy(local, bv)
	winners := SelectWinners(CanonicalOrder(p), 3, final)
	main := Bundle{
		Kind: KindMainDraw,
		Pool: p,
		Commit: CommitRecord{
			InputDigest: DigestStringsSorted(p), SeedHash: HashSeed(local), BeaconPulse: 5,
			CommittedAt: "2026-06-09T10:00:00Z",
		},
		Reveal: RevealRecord{
			Seed: hex.EncodeToString(final), LocalSeed: hex.EncodeToString(local),
			BeaconPulse: 5, BeaconValue: hex.EncodeToString(bv),
			BeaconPulseTime: "2026-06-09T10:02:00Z",
			Winners:         winners, WinnerDigest: DigestStringsOrdered(winners),
		},
	}
	if res, err := VerifyBundle(main); err != nil || !res.OK {
		t.Fatalf("main bundle: err=%v ok=%v", err, res.OK)
	}

	// INSTANT bundle with seed (reproduction-from-seed path).
	unitRefs := []string{"pool=1;type=INSTANT_CASH;amount=10", "pool=2;type=INSTANT_PHYSICAL;amount=nil"}
	il := []byte("inst-local")
	ibv := []byte("inst-beacon")
	ifin := MixExternalEntropy(il, ibv)
	alloc := AllocateInstantPrizes(1000, CanonicalOrder(unitRefs), ifin)
	allocStr := map[string]string{}
	for k, v := range alloc {
		allocStr[strconv.Itoa(k)] = v
	}
	inst := Bundle{
		Kind:       KindInstant,
		Allocation: allocStr,
		Commit:     CommitRecord{InputDigest: DigestAllocation(alloc), SeedHash: HashSeed(il), BeaconPulse: 1, CommittedAt: "2026-06-09T08:00:00Z"},
		Reveal:     RevealRecord{Seed: hex.EncodeToString(ifin), LocalSeed: hex.EncodeToString(il), BeaconPulse: 1, BeaconValue: hex.EncodeToString(ibv), TotalTickets: 1000},
	}
	if res, err := VerifyBundle(inst); err != nil || !res.OK {
		t.Fatalf("instant bundle: err=%v ok=%v", err, res.OK)
	}

	// Unknown kind errors.
	if _, err := VerifyBundle(Bundle{Kind: "NONSENSE"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestParseIntKeyMap(t *testing.T) {
	out := parseIntKeyMap(map[string]string{"7": "a", "42": "b", "bad": "c"})
	if out[7] != "a" || out[42] != "b" || len(out) != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
}
