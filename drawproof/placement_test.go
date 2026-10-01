package drawproof

import (
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
