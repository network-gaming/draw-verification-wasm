package drawproof

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func f(v float64) *float64 { return &v }

const (
	topRef   = "pool=41;type=INSTANT_CASH;amount=5000"
	midRef   = "pool=42;type=INSTANT_CASH;amount=100"
	smallRef = "pool=43;type=INSTANT_SITE_CREDIT;amount=10"
	physRef  = "pool=44;type=INSTANT_PHYSICAL;amount=nil"
)

func testUnits() []string {
	var u []string
	u = append(u, topRef)
	for i := 0; i < 5; i++ {
		u = append(u, midRef)
	}
	for i := 0; i < 40; i++ {
		u = append(u, smallRef)
	}
	u = append(u, physRef, physRef)
	return CanonicalOrder(u)
}

func topHalfRules() InstantRules {
	return InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.5, MaxFraction: 1.0},
		{Type: RuleExclude, MinAmount: f(100), MinFraction: 0.95, MaxFraction: 1.0},
	}}
}

func seedN(n int) []byte {
	s := sha256.Sum256([]byte("seed-" + strconv.Itoa(n)))
	return s[:]
}

func TestValidateRules(t *testing.T) {
	bad := []InstantRules{
		{Version: 1},
		{Version: 2, Rules: []PositionRule{{Type: RuleWindow, MinFraction: 0.2, MaxFraction: 0.9}}},                               // no selector
		{Version: 2, Rules: []PositionRule{{Type: RuleWindow, UnitRef: topRef, MinAmount: f(1), MinFraction: 0, MaxFraction: 1}}}, // two selectors
		{Version: 2, Rules: []PositionRule{{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.6, MaxFraction: 0.5}}},
		{Version: 2, Rules: []PositionRule{{Type: RuleMinGap, UnitRef: topRef, Gap: 0}}},
		{Version: 2, Rules: []PositionRule{{Type: RuleDensity, UnitRef: topRef, SegmentFraction: 0, MaxUnits: 1}}},
		{Version: 2, Rules: []PositionRule{{Type: "SOMETHING", UnitRef: topRef}}},
	}
	for i, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
	if err := topHalfRules().Validate(); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}

func TestCanonicalRulesIsOrderIndependentAndStable(t *testing.T) {
	a := topHalfRules()
	b := InstantRules{Version: 2, Rules: []PositionRule{a.Rules[1], a.Rules[0]}, MaxAttempts: 1000}
	ja, da, err := CanonicalRules(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, db, err := CanonicalRules(b)
	if err != nil {
		t.Fatal(err)
	}
	if ja != jb || da != db {
		t.Fatalf("canonical form depends on order or default maxAttempts:\n%s\n%s", ja, jb)
	}
	if strings.Contains(ja, " ") || strings.Contains(ja, "\n") {
		t.Fatalf("canonical JSON contains whitespace: %s", ja)
	}
	// Round-trip through ParseRules reproduces the same digest.
	parsed, err := ParseRules(ja)
	if err != nil {
		t.Fatal(err)
	}
	_, dc, _ := CanonicalRules(parsed)
	if dc != da {
		t.Fatalf("digest changed after parse round-trip")
	}
	// Fixed vector so a re-implementation in another language can be checked.
	const want = `{"version":2,"rules":[{"type":"EXCLUDE","minAmount":100,"minFraction":0.95,"maxFraction":1},{"type":"WINDOW","unitRef":"pool=41;type=INSTANT_CASH;amount=5000","minFraction":0.5,"maxFraction":1}],"maxAttempts":1000}`
	if ja != want {
		t.Fatalf("canonical JSON drifted:\n got %s\nwant %s", ja, want)
	}
}

func TestWindowPositions(t *testing.T) {
	lo, hi := WindowPositions(10000, 0.5, 1.0)
	if lo != 5001 || hi != 10000 {
		t.Fatalf("got [%d,%d]", lo, hi)
	}
	lo, hi = WindowPositions(7, 0, 0.5)
	if lo != 1 || hi != 3 {
		t.Fatalf("got [%d,%d]", lo, hi)
	}
}

func TestAllocateV2IsReproducibleAndHonoursRules(t *testing.T) {
	const M = 1000
	units := testUnits()
	rules := topHalfRules()
	a, at1, err := AllocateInstantPrizesV2(M, units, rules, seedN(1))
	if err != nil {
		t.Fatal(err)
	}
	b, at2, err := AllocateInstantPrizesV2(M, units, rules, seedN(1))
	if err != nil {
		t.Fatal(err)
	}
	if !allocationsEqual(a, b) || at1 != at2 || at1 != 1 {
		t.Fatal("same inputs must reproduce the same placement in one attempt")
	}
	if len(a) != len(units) {
		t.Fatalf("placed %d of %d units", len(a), len(units))
	}
	if err := CheckRules(M, a, rules); err != nil {
		t.Fatal(err)
	}
	c, _, _ := AllocateInstantPrizesV2(M, units, rules, seedN(2))
	if allocationsEqual(a, c) {
		t.Fatal("different seeds produced identical placements")
	}
}

func TestAllocateV2WindowNeverViolatedAcrossManySeeds(t *testing.T) {
	const M = 400
	units := testUnits()
	rules := topHalfRules()
	lo, _ := WindowPositions(M, 0.5, 1.0)
	exLo, _ := WindowPositions(M, 0.95, 1.0)
	for s := 0; s < 2000; s++ {
		placement, _, err := AllocateInstantPrizesV2(M, units, rules, seedN(s))
		if err != nil {
			t.Fatal(err)
		}
		for p, ref := range placement {
			if ref == topRef && p < lo {
				t.Fatalf("seed %d: top prize at %d before window start %d", s, p, lo)
			}
			if (ref == topRef || ref == midRef) && p >= exLo {
				t.Fatalf("seed %d: %s at %d inside excluded tail", s, ref, p)
			}
		}
	}
}

// Chi-square at 99% over 10 bins (9 dof): critical value 21.666.
func TestAllocateV2UniformWithinWindow(t *testing.T) {
	const M = 1000
	rules := InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.5, MaxFraction: 1.0},
	}}
	units := []string{topRef}
	lo, hi := WindowPositions(M, 0.5, 1.0)
	const draws = 20000
	bins := make([]int, 10)
	width := float64(hi-lo+1) / 10
	for s := 0; s < draws; s++ {
		placement, _, err := AllocateInstantPrizesV2(M, units, rules, seedN(s))
		if err != nil {
			t.Fatal(err)
		}
		for p := range placement {
			b := int(float64(p-lo) / width)
			if b > 9 {
				b = 9
			}
			bins[b]++
		}
	}
	expected := float64(draws) / 10
	chi := 0.0
	for _, c := range bins {
		d := float64(c) - expected
		chi += d * d / expected
	}
	if chi > 21.666 {
		t.Fatalf("placement not uniform within window: chi-square %.2f, bins %v", chi, bins)
	}
}

func TestFeasibility(t *testing.T) {
	units := []string{topRef, topRef, topRef}
	// Three units into a window of two positions.
	rules := InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.8, MaxFraction: 1.0},
	}}
	err := CheckFeasibility(10, units, rules)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible, got %v", err)
	}
	// Worst-case overlap: a more constrained group can fill the shared window.
	overlap := InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.9, MaxFraction: 1.0}, // positions 10..10 (1 slot)
		{Type: RuleWindow, UnitRef: midRef, MinFraction: 0.9, MaxFraction: 1.0},
	}}
	if err := CheckFeasibility(10, []string{topRef, midRef}, overlap); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible for shared single slot, got %v", err)
	}
	// Feasibility never consumes randomness: placement with an infeasible set fails before seeding.
	if _, _, err := AllocateInstantPrizesV2(10, units, rules, seedN(1)); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected ErrInfeasible from allocate, got %v", err)
	}
	// Gap bound.
	gap := InstantRules{Version: 2, Rules: []PositionRule{{Type: RuleMinGap, UnitRef: topRef, Gap: 6}}}
	if err := CheckFeasibility(10, units, gap); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected gap infeasible, got %v", err)
	}
}

func TestRejectionRulesReplayExactly(t *testing.T) {
	const M = 200
	var units []string
	for i := 0; i < 8; i++ {
		units = append(units, midRef)
	}
	rules := InstantRules{Version: 2, MaxAttempts: 5000, Rules: []PositionRule{
		{Type: RuleMinGap, UnitRef: midRef, Gap: 15},
		{Type: RuleDensity, MinAmount: f(50), SegmentFraction: 0.25, MaxUnits: 3},
	}}
	sawRetry := false
	for s := 0; s < 200; s++ {
		a, at, err := AllocateInstantPrizesV2(M, units, rules, seedN(s))
		if err != nil {
			t.Fatalf("seed %d: %v", s, err)
		}
		if at > 1 {
			sawRetry = true
		}
		if err := CheckRules(M, a, rules); err != nil {
			t.Fatalf("seed %d: %v", s, err)
		}
		b, at2, _ := AllocateInstantPrizesV2(M, units, rules, seedN(s))
		if !allocationsEqual(a, b) || at != at2 {
			t.Fatalf("seed %d: replay diverged (attempts %d vs %d)", s, at, at2)
		}
	}
	if !sawRetry {
		t.Fatal("expected at least one seed to need more than one attempt")
	}
	tight := InstantRules{Version: 2, MaxAttempts: 3, Rules: []PositionRule{
		{Type: RuleMinGap, UnitRef: midRef, Gap: 25}, // 8 units * 25 = exactly fits, near-impossible randomly
	}}
	if _, _, err := AllocateInstantPrizesV2(M, units, tight, seedN(1)); !errors.Is(err, ErrAttemptsExhausted) {
		t.Fatalf("expected ErrAttemptsExhausted, got %v", err)
	}
}

func TestDisplayPermutationIsBijectionAndSeedSeparated(t *testing.T) {
	const M = 5000
	perm := DisplayPermutation(M, seedN(7))
	seen := make([]bool, M+1)
	for _, n := range perm {
		if n < 1 || n > M || seen[n] {
			t.Fatalf("not a permutation: %d", n)
		}
		seen[n] = true
	}
	again := DisplayPermutation(M, seedN(7))
	if DigestPermutation(perm) != DigestPermutation(again) {
		t.Fatal("permutation not reproducible")
	}
	// The placement DRBG and the display DRBG must not share a stream: the
	// first placement position must not equal the first display value for the
	// trivially-seeded single-unit case in more than a chance proportion.
	same := 0
	for s := 0; s < 200; s++ {
		p, _, _ := AllocateInstantPrizesV2(M, []string{smallRef}, InstantRules{Version: 2}, seedN(s))
		d := DisplayPermutation(M, seedN(s))
		for pos := range p {
			if pos == d[0] {
				same++
			}
		}
	}
	if same > 5 {
		t.Fatalf("display and placement streams look correlated: %d/200 coincidences", same)
	}
}

func TestPreviewNeverExposesSeedAndSummarises(t *testing.T) {
	const M = 1000
	placement, stats, err := PreviewInstantPrizes(M, testUnits(), topHalfRules())
	if err != nil {
		t.Fatal(err)
	}
	if len(placement) != len(testUnits()) || stats.Attempts != 1 || len(stats.PerSegment) != 10 {
		t.Fatalf("unexpected preview: %d placed, stats %+v", len(placement), stats)
	}
	total := 0
	for _, c := range stats.PerSegment {
		total += c
	}
	if total != len(placement) {
		t.Fatalf("segment counts %d != placements %d", total, len(placement))
	}
	for _, u := range stats.PerUnit {
		if u.UnitRef == topRef && u.First <= M/2 {
			t.Fatalf("top prize previewed at %d, before the window", u.First)
		}
	}
}

func buildV2Bundle(t *testing.T, M int, units []string, rules InstantRules, seedIdx int) Bundle {
	t.Helper()
	local := seedN(seedIdx)
	beacon := sha256.Sum256([]byte("beacon"))
	final := MixExternalEntropy(local, beacon[:])
	placement, attempts, err := AllocateInstantPrizesV2(M, units, rules, final)
	if err != nil {
		t.Fatal(err)
	}
	perm := DisplayPermutation(M, final)
	rulesJSON, rulesDigest, _ := CanonicalRules(rules)
	alloc := map[string]string{}
	for p, ref := range placement {
		alloc[strconv.Itoa(p)] = ref
	}
	return Bundle{
		Kind:          KindInstant,
		Allocation:    alloc,
		Rules:         rulesJSON,
		TicketNumbers: perm,
		Commit: CommitRecord{
			Kind: KindInstant, RoundID: 1, AlgorithmVersion: 2,
			InputDigest: DigestAllocation(placement), RulesDigest: rulesDigest,
			DisplayDigest: DigestPermutation(perm), TotalTickets: M,
			SeedHash: HashSeed(local), BeaconPulse: 99, CommittedAt: "2026-09-14T09:02:11Z",
		},
		Reveal: RevealRecord{
			Kind: KindInstant, RoundID: 1, Seed: hex.EncodeToString(final), LocalSeed: hex.EncodeToString(local),
			BeaconPulse: 99, BeaconValue: hex.EncodeToString(beacon[:]), WinnerDigest: DigestAllocation(placement),
			TotalTickets: M, Attempts: attempts, ResultedAt: "2026-09-20T18:00:04Z",
		},
	}
}

func failing(res VerifyResult) []string {
	var out []string
	for _, c := range res.Checks {
		if !c.OK {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}

func TestVerifyV2Bundle(t *testing.T) {
	const M = 800
	b := buildV2Bundle(t, M, testUnits(), topHalfRules(), 3)
	res, err := VerifyBundle(b)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("valid bundle failed: %v", failing(res))
	}
	names := map[string]bool{}
	for _, c := range res.Checks {
		names[c.Name] = true
	}
	for _, want := range []string{"rules-digest-matches-commit", "allocation-digest-matches-commit", "local-seed-hash-matches-commit",
		"beacon-mix", "allocation-reproduced-from-seed", "rules-satisfied", "display-permutation-matches-commit", "ticket-numbers-match-permutation"} {
		if !names[want] {
			t.Errorf("missing check %s", want)
		}
	}

	// Edited rule -> check 1 fails.
	edited := b
	edited.Rules = strings.Replace(b.Rules, `"minFraction":0.5`, `"minFraction":0.4`, 1)
	res, _ = VerifyBundle(edited)
	if res.OK || !strings.HasPrefix(failing(res)[0], "rules-digest-matches-commit") {
		t.Fatalf("edited rules should fail at rules digest: %v", failing(res))
	}

	// Edited allocation -> check 2 fails (and reproduction).
	tampered := b
	tampered.Allocation = map[string]string{}
	for k, v := range b.Allocation {
		tampered.Allocation[k] = v
	}
	for k, v := range tampered.Allocation {
		if v == topRef {
			delete(tampered.Allocation, k)
			tampered.Allocation["1"] = topRef // move the top prize to the first sale
			break
		}
	}
	res, _ = VerifyBundle(tampered)
	if res.OK {
		t.Fatal("tampered allocation verified")
	}
	f := strings.Join(failing(res), "\n")
	if !strings.Contains(f, "allocation-digest-matches-commit") || !strings.Contains(f, "rules-satisfied") {
		t.Fatalf("expected digest and rule failures, got:\n%s", f)
	}

	// Sealed bundle (no reveal yet) verifies the commit shape only.
	sealed := b
	sealed.Reveal = RevealRecord{}
	sealed.Allocation = nil
	res, _ = VerifyBundle(sealed)
	if !res.OK {
		t.Fatalf("sealed bundle should pass its reduced checks: %v", failing(res))
	}
}

func TestV1BundlesStillVerify(t *testing.T) {
	const M = 300
	local := seedN(11)
	units := CanonicalOrder([]string{smallRef, smallRef, midRef})
	placement := AllocateInstantPrizes(M, units, local)
	alloc := map[string]string{}
	for p, ref := range placement {
		alloc[strconv.Itoa(p)] = ref
	}
	b := Bundle{
		Kind: KindInstant, Allocation: alloc,
		Commit: CommitRecord{Kind: KindInstant, RoundID: 2, InputDigest: DigestAllocation(placement), SeedHash: HashSeed(local), CommittedAt: "2026-01-01T00:00:00Z"},
		Reveal: RevealRecord{Kind: KindInstant, RoundID: 2, Seed: hex.EncodeToString(local), LocalSeed: hex.EncodeToString(local), WinnerDigest: DigestAllocation(placement), TotalTickets: M},
	}
	res, err := VerifyBundle(b)
	if err != nil || !res.OK {
		t.Fatalf("v1 bundle failed: %v %v", err, failing(res))
	}
}

func TestSummarisePlacementSegments(t *testing.T) {
	stats := SummarisePlacement(100, map[int]string{1: "a", 100: "a", 50: "b"}, 1)
	if stats.PerSegment[0] != 1 || stats.PerSegment[9] != 1 || stats.PerSegment[4] != 1 {
		t.Fatalf("segments %v", stats.PerSegment)
	}
	if math.Abs(float64(stats.Attempts-1)) > 0 {
		t.Fatal("attempts")
	}
}

func TestQuotaFromPlacementRoundTrips(t *testing.T) {
	const M = 1200
	units := testUnits()
	// A candidate on one seed, unconstrained.
	candidate, _, err := AllocateInstantPrizesV2(M, units, InstantRules{Version: 2}, seedN(21))
	if err != nil {
		t.Fatal(err)
	}
	quota := QuotaFromPlacement(M, candidate, QuotaSegments)
	rules := InstantRules{Version: 2, Rules: quota}
	if err := rules.Validate(); err != nil {
		t.Fatal(err)
	}
	// Every unit reference gets exactly one QUOTA whose counts sum to its units.
	perRef := map[string]int{}
	for _, u := range units {
		perRef[u]++
	}
	if len(quota) != len(perRef) {
		t.Fatalf("expected %d quota rules, got %d", len(perRef), len(quota))
	}
	for _, q := range quota {
		sum := 0
		for _, c := range q.Counts {
			sum += c
		}
		if sum != perRef[q.UnitRef] || len(q.Counts) != QuotaSegments {
			t.Fatalf("quota for %s: counts %v vs %d units", q.UnitRef, q.Counts, perRef[q.UnitRef])
		}
	}
	// The candidate itself satisfies its derived quota.
	if err := CheckRules(M, candidate, rules); err != nil {
		t.Fatal(err)
	}
	// A fresh seed under the quota reproduces the bracket distribution but not the candidate.
	fresh, attempts, err := AllocateInstantPrizesV2(M, units, rules, seedN(22))
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("quota placement must be direct, got %d attempts", attempts)
	}
	if err := CheckRules(M, fresh, rules); err != nil {
		t.Fatal(err)
	}
	if allocationsEqual(fresh, candidate) {
		t.Fatal("fresh seed reproduced the candidate exactly; the quota should only fix brackets")
	}
	again := QuotaFromPlacement(M, fresh, QuotaSegments)
	_, d1, _ := CanonicalRules(rules)
	_, d2, _ := CanonicalRules(InstantRules{Version: 2, Rules: again})
	if d1 != d2 {
		t.Fatal("bracket distribution changed under the quota")
	}
	// Canonical form survives a parse round-trip.
	canon, digest, _ := CanonicalRules(rules)
	parsed, err := ParseRules(canon)
	if err != nil {
		t.Fatal(err)
	}
	if _, d3, _ := CanonicalRules(parsed); d3 != digest {
		t.Fatal("quota canonical digest not stable")
	}
}

func TestQuotaFeasibilityAndBounds(t *testing.T) {
	// Brackets partition the sale exactly.
	for _, M := range []int{7, 10, 1200, 1001} {
		seen := 0
		for k := 0; k < QuotaSegments; k++ {
			lo, hi := QuotaSegmentBounds(M, QuotaSegments, k)
			seen += hi - lo + 1
			for p := lo; p <= hi; p++ {
				if QuotaSegmentOf(M, QuotaSegments, p) != k {
					t.Fatalf("M=%d position %d not in bracket %d", M, p, k)
				}
			}
		}
		if seen != M {
			t.Fatalf("M=%d brackets cover %d positions", M, seen)
		}
	}
	units := []string{topRef, topRef}
	// Counts must add up to the units.
	bad := InstantRules{Version: 2, Rules: []PositionRule{{Type: RuleQuota, UnitRef: topRef, Segments: 10, Counts: []int{1, 0, 0, 0, 0, 0, 0, 0, 0, 0}}}}
	if err := CheckFeasibility(100, units, bad); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected infeasible for quota sum mismatch, got %v", err)
	}
	// More units in a bracket than it has positions.
	tight := InstantRules{Version: 2, Rules: []PositionRule{{Type: RuleQuota, UnitRef: topRef, Segments: 10, Counts: []int{2, 0, 0, 0, 0, 0, 0, 0, 0, 0}}}}
	if err := CheckFeasibility(10, units, tight); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected infeasible for overfull bracket, got %v", err)
	}
	// Two quotas matching the same unit are rejected.
	two := InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleQuota, UnitRef: topRef, Segments: 10, Counts: []int{2, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{Type: RuleQuota, MinAmount: f(1), Segments: 10, Counts: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 2}},
	}}
	if err := CheckFeasibility(100, units, two); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("expected infeasible for overlapping quotas, got %v", err)
	}
	// Quota composes with a window: bracket 10 of 100 is positions 91..100; a
	// window [0.95,1] leaves 96..100, still room for 2.
	both := InstantRules{Version: 2, Rules: []PositionRule{
		{Type: RuleQuota, UnitRef: topRef, Segments: 10, Counts: []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 2}},
		{Type: RuleWindow, UnitRef: topRef, MinFraction: 0.95, MaxFraction: 1},
	}}
	placement, _, err := AllocateInstantPrizesV2(100, units, both, seedN(3))
	if err != nil {
		t.Fatal(err)
	}
	for p := range placement {
		if p < 96 {
			t.Fatalf("position %d outside quota∩window", p)
		}
	}
	// Verifier path: a bundle under quota rules verifies, and a moved prize fails rules-satisfied.
	b := buildV2Bundle(t, 500, testUnits(), InstantRules{Version: 2, Rules: QuotaFromPlacement(500, mustAlloc(t, 500, testUnits(), seedN(9)), QuotaSegments)}, 5)
	res, _ := VerifyBundle(b)
	if !res.OK {
		t.Fatalf("quota bundle failed: %v", failing(res))
	}
}

func mustAlloc(t *testing.T, M int, units []string, seed []byte) map[int]string {
	t.Helper()
	p, _, err := AllocateInstantPrizesV2(M, units, InstantRules{Version: 2}, seed)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
