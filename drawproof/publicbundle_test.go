package drawproof

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// publicBundle strips an audit bundle down to what the platform publishes to
// players: the records, the placement, no rules. The placement is returned as
// the allocation file text as well, in ascending position order.
func publicBundle(t *testing.T, audit Bundle) (Bundle, string) {
	t.Helper()
	pub := audit
	pub.Rules = ""
	pub.TicketNumbers = nil
	positions := make([]int, 0, len(audit.Allocation))
	for k := range audit.Allocation {
		n, err := strconv.Atoi(k)
		if err != nil {
			t.Fatal(err)
		}
		positions = append(positions, n)
	}
	sort.Ints(positions)
	var sb strings.Builder
	for _, p := range positions {
		sb.WriteString(AllocationLine(p, audit.Allocation[strconv.Itoa(p)]))
	}
	return pub, sb.String()
}

func checkNames(res VerifyResult) map[string]bool {
	out := map[string]bool{}
	for _, c := range res.Checks {
		out[c.Name] = c.OK
	}
	return out
}

func TestVerifyV2Bundle_RulesWithheld(t *testing.T) {
	const M = 800
	audit := buildV2Bundle(t, M, testUnits(), topHalfRules(), 5)
	pub, _ := publicBundle(t, audit)

	res, err := VerifyBundle(pub)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("public bundle failed: %v", failing(res))
	}
	names := checkNames(res)
	for _, want := range []string{"rules-withheld", "allocation-digest-matches-commit", "local-seed-hash-matches-commit", "beacon-mix", "display-permutation-matches-commit"} {
		if ok, present := names[want]; !present || !ok {
			t.Errorf("expected passing check %s, got %v", want, res.Checks)
		}
	}
	for _, absent := range []string{"rules-digest-matches-commit", "rules-satisfied", "allocation-reproduced-from-seed"} {
		if _, present := names[absent]; present {
			t.Errorf("check %s must not run without the rules", absent)
		}
	}

	// Moving one prize breaks the binding to the commitment.
	tampered := pub
	tampered.Allocation = map[string]string{}
	for k, v := range pub.Allocation {
		tampered.Allocation[k] = v
	}
	var from string
	for k := range tampered.Allocation {
		from = k
		break
	}
	ref := tampered.Allocation[from]
	delete(tampered.Allocation, from)
	for p := 1; p <= M; p++ {
		if _, taken := tampered.Allocation[strconv.Itoa(p)]; !taken {
			tampered.Allocation[strconv.Itoa(p)] = ref
			break
		}
	}
	res, _ = VerifyBundle(tampered)
	if res.OK || checkNames(res)["allocation-digest-matches-commit"] {
		t.Fatalf("tampered public placement verified: %v", res.Checks)
	}
}

func TestVerifyV2Bundle_RulesWithheldNeedsThePlacement(t *testing.T) {
	audit := buildV2Bundle(t, 800, testUnits(), topHalfRules(), 6)
	pub, _ := publicBundle(t, audit)
	// Counts only, rules withheld: nothing to bind to the commitment.
	pub.Units = CountUnits(testUnits())
	pub.Allocation = nil
	res, err := VerifyBundle(pub)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || checkNames(res)["allocation-digest-matches-commit"] {
		t.Fatalf("counts-only bundle without rules must fail: %v", res.Checks)
	}
}

func TestAllocationFeed_FileRoundTrip(t *testing.T) {
	const M = 800
	audit := buildV2Bundle(t, M, testUnits(), topHalfRules(), 7)
	pub, file := publicBundle(t, audit)
	pub.Allocation = nil
	pub.AllocationFile = "placement.txt"
	pub.AllocationCount = len(audit.Allocation)

	// Unloaded, VerifyBundle says so rather than failing the checks.
	if _, err := VerifyBundle(pub); !errors.Is(err, ErrAllocationNotLoaded) {
		t.Fatalf("expected ErrAllocationNotLoaded, got %v", err)
	}

	// Fed in uneven chunks split on line ends, as the worker does.
	var feed AllocationFeed
	lines := strings.SplitAfter(file, "\n")
	for i := 0; i < len(lines); i += 7 {
		end := i + 7
		if end > len(lines) {
			end = len(lines)
		}
		if err := feed.Feed(strings.Join(lines[i:end], "")); err != nil {
			t.Fatal(err)
		}
	}
	if feed.Len() != pub.AllocationCount {
		t.Fatalf("fed %d prizes, file has %d", feed.Len(), pub.AllocationCount)
	}
	placement, err := feed.Placement(M)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := PlacementFromMap(M, parseIntKeyMap(audit.Allocation))
	if !placement.Equal(want) {
		t.Fatal("placement read from the file differs from the allocation")
	}
	res := VerifyInstantV2Placement(pub, placement)
	if !res.OK {
		t.Fatalf("public bundle with file placement failed: %v", failing(res))
	}
	if !checkNames(res)["rules-withheld"] {
		t.Fatalf("expected rules-withheld: %v", res.Checks)
	}

	// The audit bundle still gets the full replay through the same entry point.
	full := VerifyInstantV2Placement(audit, placement)
	if !full.OK || !checkNames(full)["allocation-reproduced-from-seed"] || !checkNames(full)["rules-satisfied"] {
		t.Fatalf("audit bundle over a fed placement: %v", failing(full))
	}
}

func TestAllocationFeed_Parsing(t *testing.T) {
	var f AllocationFeed
	if err := f.Feed("1 pool=1;type=CASH;amount=5\r\n\n  12\tpool=2;type=CASH;amount=10  \n3 pool=1;type=CASH;amount=5\n"); err != nil {
		t.Fatal(err)
	}
	if f.Len() != 3 {
		t.Fatalf("len %d", f.Len())
	}
	m := f.Map()
	if m[1] != "pool=1;type=CASH;amount=5" || m[12] != "pool=2;type=CASH;amount=10" || m[3] != "pool=1;type=CASH;amount=5" {
		t.Fatalf("parsed %v", m)
	}
	for _, bad := range []string{"x pool=1\n", "7\n", "7   \n"} {
		var g AllocationFeed
		if err := g.Feed(bad); err == nil {
			t.Errorf("line %q should not parse", bad)
		}
	}
	// Positions outside the population or repeated are rejected when the
	// placement is built, not silently merged.
	var g AllocationFeed
	_ = g.Feed("5 a\n5 b\n")
	if _, err := g.Placement(10); err == nil {
		t.Error("repeated position should fail")
	}
	var h AllocationFeed
	_ = h.Feed("11 a\n")
	if _, err := h.Placement(10); err == nil {
		t.Error("position past the population should fail")
	}
}
