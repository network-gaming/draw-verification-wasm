package drawproof

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// The interval/rank implementation must reproduce v0.3.0 placement for
// placement, seed for seed: rounds sealed under v0.3.0 are replayed with this
// code at disclosure and by every verifier.

func randomRules(r *rand.Rand, refs []string, allowRejection bool) InstantRules {
	rules := InstantRules{Version: AlgorithmVersionV2}
	n := r.Intn(4)
	for i := 0; i < n; i++ {
		ref := refs[r.Intn(len(refs))]
		switch r.Intn(5) {
		case 0:
			lo := r.Float64() * 0.6
			rules.Rules = append(rules.Rules, PositionRule{Type: RuleWindow, UnitRef: ref, MinFraction: lo, MaxFraction: lo + 0.2 + r.Float64()*0.2})
		case 1:
			lo := r.Float64() * 0.8
			rules.Rules = append(rules.Rules, PositionRule{Type: RuleExclude, UnitRef: ref, MinFraction: lo, MaxFraction: lo + r.Float64()*0.15})
		case 2:
			amt := float64(r.Intn(50))
			lo := r.Float64() * 0.5
			rules.Rules = append(rules.Rules, PositionRule{Type: RuleWindow, MinAmount: &amt, MinFraction: lo, MaxFraction: lo + 0.3})
		case 3:
			if allowRejection {
				rules.Rules = append(rules.Rules, PositionRule{Type: RuleMinGap, UnitRef: ref, Gap: 1 + r.Intn(3)})
			}
		case 4:
			if allowRejection {
				rules.Rules = append(rules.Rules, PositionRule{Type: RuleDensity, UnitRef: ref, SegmentFraction: 0.1 + r.Float64()*0.4, MaxUnits: 1 + r.Intn(4)})
			}
		}
	}
	return rules
}

func randomUnits(r *rand.Rand, refs []string, max int) []string {
	var units []string
	for _, ref := range refs {
		n := r.Intn(max + 1)
		for i := 0; i < n; i++ {
			units = append(units, ref)
		}
	}
	return CanonicalOrder(units)
}

func refsFor(pools int) []string {
	refs := make([]string, pools)
	for i := range refs {
		refs[i] = fmt.Sprintf("pool=%d;type=INSTANT_CASH;amount=%d", i+1, (i+1)*10)
	}
	return refs
}

func TestPlacementMatchesLegacy(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	cases, skipped := 0, 0
	for iter := 0; iter < 400; iter++ {
		M := 1 + r.Intn(3000)
		refs := refsFor(1 + r.Intn(4))
		units := randomUnits(r, refs, 6)
		rules := randomRules(r, refs, false)
		// Add a quota on one reference now and then, derived from a legal placement.
		if len(units) > 0 && r.Intn(3) == 0 {
			if err := legacyCheckFeasibility(M, units, rules); err == nil {
				seed := []byte{byte(iter)}
				pl := legacyPlaceOnce(M, legacyBuildGroups(M, units, rules), newDRBG(seed))
				rules.Rules = append(rules.Rules, QuotaFromPlacement(M, pl, 2+r.Intn(5))[0])
			}
		}
		legacyErr := legacyCheckFeasibility(M, units, rules)
		newErr := CheckFeasibility(M, units, rules)
		if (legacyErr == nil) != (newErr == nil) {
			t.Fatalf("feasibility differs (M=%d units=%v rules=%+v): legacy %v, new %v", M, units, rules, legacyErr, newErr)
		}
		if legacyErr != nil {
			skipped++
			continue
		}
		for s := 0; s < 3; s++ {
			seed := []byte{byte(iter), byte(s), 42}
			want := legacyPlaceOnce(M, legacyBuildGroups(M, units, rules), newDRBG(seed))
			got := placeOnce(M, buildGroups(M, units, rules), newDRBG(seed))
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("placement differs (M=%d units=%v rules=%+v seed=%v)\nlegacy %v\nnew    %v", M, units, rules, seed, want, got)
			}
			cases++
		}
	}
	if cases < 300 {
		t.Fatalf("too few comparable cases: %d (skipped %d)", cases, skipped)
	}
}

func TestGroupSizesMatchLegacy(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for iter := 0; iter < 200; iter++ {
		M := 1 + r.Intn(500)
		refs := refsFor(1 + r.Intn(3))
		units := randomUnits(r, refs, 5)
		rules := randomRules(r, refs, true)
		lg := legacyBuildGroups(M, units, rules)
		ng := buildGroups(M, units, rules)
		if len(lg) != len(ng) {
			t.Fatalf("group count differs: %d vs %d", len(lg), len(ng))
		}
		for i := range lg {
			if lg[i].ref != ng[i].ref || lg[i].size != ng[i].size || lg[i].count != ng[i].count || lg[i].segment != ng[i].segment {
				t.Fatalf("group %d differs: legacy %+v new ref=%s size=%d count=%d seg=%d", i, *lg[i], ng[i].ref, ng[i].size, ng[i].count, ng[i].segment)
			}
			for p := 1; p <= M; p++ {
				in := false
				for _, x := range ng[i].allowed {
					if p >= x.lo && p <= x.hi {
						in = true
					}
				}
				if in != lg[i].allowed[p] {
					t.Fatalf("allowed differs at %d for group %d", p, i)
				}
			}
		}
	}
}

func TestRankSetKthFree(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for iter := 0; iter < 200; iter++ {
		M := 1 + r.Intn(2000)
		rs := newRankSet(M)
		taken := make([]bool, M+1)
		for k := 0; k < r.Intn(M+1); k++ {
			p := 1 + r.Intn(M)
			if !taken[p] {
				taken[p] = true
				rs.take(p)
			}
		}
		s := allowedSpans(M, "x", InstantRules{Version: 2, Rules: []PositionRule{
			{Type: RuleWindow, UnitRef: "x", MinFraction: r.Float64() * 0.3, MaxFraction: 0.7 + r.Float64()*0.3},
			{Type: RuleExclude, UnitRef: "x", MinFraction: 0.4, MaxFraction: 0.45},
		}})
		var free []int
		for p := 1; p <= M; p++ {
			in := false
			for _, x := range s {
				if p >= x.lo && p <= x.hi {
					in = true
				}
			}
			if in && !taken[p] {
				free = append(free, p)
			}
		}
		for k := range free {
			got, ok := rs.kthFree(s, k)
			if !ok || got != free[k] {
				t.Fatalf("kthFree(%d) = %d,%v want %d (M=%d)", k, got, ok, free[k], M)
			}
		}
		if _, ok := rs.kthFree(s, len(free)); ok {
			t.Fatalf("kthFree past the end should fail")
		}
		for p := 1; p <= M; p++ {
			want := 0
			for q := 1; q <= p; q++ {
				if taken[q] {
					want++
				}
			}
			if rs.takenUpTo(p) != want {
				t.Fatalf("takenUpTo(%d) = %d want %d", p, rs.takenUpTo(p), want)
			}
		}
	}
}

func TestSelectWinnerRanksMatchesSelectWinners(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for iter := 0; iter < 100; iter++ {
		n := 1 + r.Intn(500)
		pool := make([]string, n)
		for i := range pool {
			pool[i] = fmt.Sprintf("%d", r.Intn(1000000))
		}
		pool = CanonicalOrder(pool)
		count := r.Intn(n + 5)
		seed := []byte{byte(iter), 9}
		want := SelectWinners(pool, count, seed)
		ranks := SelectWinnerRanks(n, count, seed)
		got := make([]string, len(ranks))
		for i, k := range ranks {
			got[i] = pool[k]
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("winners differ for n=%d count=%d: %v vs %v", n, count, want, got)
		}
	}
}

func TestDigestPermutationMatchesStrings(t *testing.T) {
	perm := DisplayPermutation(5000, []byte("seed"))
	items := make([]string, len(perm))
	for i, n := range perm {
		items[i] = fmt.Sprint(n)
	}
	if DigestPermutation(perm) != DigestStringsOrdered(items) {
		t.Fatal("DigestPermutation must equal DigestStringsOrdered over the decimal strings")
	}
	if DigestPermutation(nil) != DigestStringsOrdered(nil) {
		t.Fatal("empty digests differ")
	}
}

// Ten-million-ticket mint with ten quota pools: the plan's target is under
// two seconds and well under 200 MB for the placement alone.
func TestLargeMintBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("large")
	}
	const M = 10_000_000
	refs := refsFor(10)
	var units []string
	for i, ref := range refs {
		for k := 0; k < 100*(i+1); k++ {
			units = append(units, ref)
		}
	}
	units = CanonicalOrder(units)
	rules := InstantRules{Version: AlgorithmVersionV2}
	for _, ref := range refs {
		counts := make([]int, QuotaSegments)
		n := 0
		for _, u := range units {
			if u == ref {
				n++
			}
		}
		for k := range counts {
			counts[k] = n / QuotaSegments
		}
		counts[0] += n - (n/QuotaSegments)*QuotaSegments
		rules.Rules = append(rules.Rules, PositionRule{Type: RuleQuota, UnitRef: ref, Segments: QuotaSegments, Counts: counts})
	}
	start := time.Now()
	pl, attempts, err := AllocateInstantPrizesV2(M, units, rules, []byte("bench"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pl) != len(units) || attempts != 1 {
		t.Fatalf("placed %d of %d in %d attempts", len(pl), len(units), attempts)
	}
	if err := CheckRules(M, pl, rules); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	t.Logf("10M tickets, %d units, 10 quota pools: %s", len(units), elapsed)
	if elapsed > 5*time.Second {
		t.Fatalf("placement took %s", elapsed)
	}
}

func BenchmarkAllocateV2TenMillion(b *testing.B) {
	const M = 10_000_000
	refs := refsFor(10)
	var units []string
	for _, ref := range refs {
		for k := 0; k < 500; k++ {
			units = append(units, ref)
		}
	}
	units = CanonicalOrder(units)
	rules := InstantRules{Version: AlgorithmVersionV2}
	for _, ref := range refs {
		counts := make([]int, QuotaSegments)
		for k := range counts {
			counts[k] = 50
		}
		rules.Rules = append(rules.Rules, PositionRule{Type: RuleQuota, UnitRef: ref, Segments: QuotaSegments, Counts: counts})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, _, err := AllocateInstantPrizesV2(M, units, rules, []byte("bench")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDisplayPermutationTenMillion(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		perm := DisplayPermutation(10_000_000, []byte("bench"))
		_ = DigestPermutation(perm)
	}
}
