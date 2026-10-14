package drawproof

import (
	"encoding/hex"
	"math/rand"
	"reflect"
	"testing"
)

// The compact Placement must be the map form in every respect the platform
// relies on: same draw from the same seed, same digest (ledger commits hold
// it), same quota derivation, same summary and the same rule verdicts.
func TestPlacementMatchesMapForm(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for iter := 0; iter < 300; iter++ {
		M := 1 + r.Intn(2000)
		refs := refsFor(1 + r.Intn(4))
		units := randomUnits(r, refs, 6)
		rules := randomRules(r, refs, false)
		if err := CheckFeasibility(M, units, rules); err != nil {
			continue
		}
		seed := []byte{byte(iter), 7}
		wantMap, wantAttempts, err := AllocateInstantPrizesV2(M, units, rules, seed)
		if err != nil {
			t.Fatal(err)
		}
		got, attempts, err := AllocatePlacement(M, CountUnits(units), rules, seed)
		if err != nil {
			t.Fatal(err)
		}
		if attempts != wantAttempts || !reflect.DeepEqual(got.Map(), wantMap) {
			t.Fatalf("placement differs (M=%d units=%v seed=%v)", M, units, seed)
		}
		if got.Len() != len(wantMap) {
			t.Fatalf("Len %d, map has %d", got.Len(), len(wantMap))
		}
		if got.Digest() != DigestAllocation(wantMap) {
			t.Fatalf("digest differs (M=%d units=%v)", M, units)
		}
		if !reflect.DeepEqual(got.Quota(QuotaSegments), QuotaFromPlacement(M, wantMap, QuotaSegments)) {
			t.Fatalf("quota differs (M=%d units=%v)", M, units)
		}
		if !reflect.DeepEqual(got.Summarise(attempts), SummarisePlacement(M, wantMap, attempts)) {
			t.Fatalf("summary differs (M=%d units=%v)", M, units)
		}
		mapErr := CheckRules(M, wantMap, rules)
		pErr := got.CheckRules(rules)
		if (mapErr == nil) != (pErr == nil) {
			t.Fatalf("rule verdict differs: map %v, placement %v", mapErr, pErr)
		}
		back, err := PlacementFromMap(M, wantMap)
		if err != nil {
			t.Fatal(err)
		}
		if !back.Equal(got) || !got.Equal(back) {
			t.Fatalf("round trip through the map form differs")
		}
		if !reflect.DeepEqual(got.Counts(), CountUnits(units)) {
			t.Fatalf("counts differ")
		}
	}
}

func TestPlacementSetAndRange(t *testing.T) {
	p := NewPlacement(5)
	if err := p.Set(2, "b"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set(4, "a"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set(4, "c"); err != nil { // replace
		t.Fatal(err)
	}
	if err := p.Set(0, "x"); err == nil {
		t.Fatal("position 0 accepted")
	}
	if err := p.Set(6, "x"); err == nil {
		t.Fatal("position past the end accepted")
	}
	if p.Len() != 2 {
		t.Fatalf("Len %d", p.Len())
	}
	var seen []int
	p.Range(func(pos int, ref string) bool {
		seen = append(seen, pos)
		return true
	})
	if !reflect.DeepEqual(seen, []int{2, 4}) {
		t.Fatalf("range order %v", seen)
	}
	if ref, ok := p.Ref(4); !ok || ref != "c" {
		t.Fatalf("Ref(4) = %q %v", ref, ok)
	}
	if err := p.Set(2, ""); err != nil {
		t.Fatal(err)
	}
	if p.Len() != 1 {
		t.Fatalf("Len after clear %d", p.Len())
	}
	if _, err := PlacementFromMap(3, map[int]string{9: "a"}); err == nil {
		t.Fatal("out-of-range map position accepted")
	}
}

// Peak live heap of a ten-million-ticket placement with seven million prizes
// in three pools: the structures are the permutation-free Placement (20 MB),
// the swap scratch (40 MB) and the rank set, well under a 500 MB task.
func TestPlacementTenMillionMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	const M = 10_000_000
	units := UnitCounts{"a": 3_000_000, "b": 2_000_000, "c": 2_000_000}
	p, _, err := AllocatePlacement(M, units, InstantRules{Version: AlgorithmVersionV2}, []byte("mem"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Len() != 7_000_000 {
		t.Fatalf("placed %d", p.Len())
	}
	_ = p.Digest()
}

// A v2 bundle that publishes unit counts instead of positions verifies by
// reproducing the allocation and checking its digest against the commit; a
// tampered commit digest or count fails it.
func TestVerifyInstantV2FromUnitCounts(t *testing.T) {
	const M = 500
	units := UnitCounts{"pool=1;type=INSTANT_CASH;amount=5": 40, "pool=2;type=INSTANT_CASH;amount=50": 3}
	rules := InstantRules{Version: AlgorithmVersionV2, Rules: []PositionRule{
		{Type: RuleWindow, UnitRef: "pool=2;type=INSTANT_CASH;amount=50", MinFraction: 0.5, MaxFraction: 1},
	}}
	canon, rulesDigest, err := CanonicalRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	local := []byte("local-seed-for-counts-test-0001")
	placement, attempts, err := AllocatePlacement(M, units, rules, local)
	if err != nil {
		t.Fatal(err)
	}
	perm := DisplayPermutation(M, local)
	bundle := Bundle{
		Kind:  KindInstant,
		Units: map[string]int(units),
		Rules: canon,
		Commit: CommitRecord{
			Kind: KindInstant, AlgorithmVersion: AlgorithmVersionV2, TotalTickets: M,
			InputDigest: placement.Digest(), RulesDigest: rulesDigest, DisplayDigest: DigestPermutation(perm),
			SeedHash: HashSeed(local), CommittedAt: "2026-01-01T00:00:00Z",
		},
		Reveal: RevealRecord{
			Kind: KindInstant, Seed: hex.EncodeToString(local), LocalSeed: hex.EncodeToString(local),
			WinnerDigest: placement.Digest(), TotalTickets: M, Attempts: attempts,
		},
	}
	res, err := VerifyBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("bundle from unit counts failed: %+v", res.Checks)
	}
	for _, name := range []string{"allocation-digest-matches-commit", "allocation-reproduced-from-seed", "rules-satisfied"} {
		found := false
		for _, c := range res.Checks {
			if c.Name == name {
				found = true
				if !c.OK {
					t.Fatalf("check %s failed: %s", name, c.Detail)
				}
			}
		}
		if !found {
			t.Fatalf("check %s missing", name)
		}
	}

	bad := bundle
	bad.Commit.InputDigest = "0000"
	if res, _ := VerifyBundle(bad); res.OK {
		t.Fatal("tampered commit digest verified")
	}
	bad = bundle
	bad.Units = map[string]int{"pool=1;type=INSTANT_CASH;amount=5": 41, "pool=2;type=INSTANT_CASH;amount=50": 3}
	if res, _ := VerifyBundle(bad); res.OK {
		t.Fatal("tampered unit count verified")
	}
}
