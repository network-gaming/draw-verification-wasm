package drawproof

import (
	"fmt"
	"math"
	"testing"
)

func pool(n int) []string {
	p := make([]string, n)
	for i := range p {
		p[i] = fmt.Sprintf("entry-%d", i+1)
	}
	return p
}

// Determinism: same (pool, seed) always yields identical winners.
func TestSelectWinners_Deterministic(t *testing.T) {
	seed := []byte("a fixed seed value for the draw")
	p := pool(1000)
	a := SelectWinners(p, 10, seed)
	b := SelectWinners(p, 10, seed)
	if len(a) != 10 {
		t.Fatalf("expected 10 winners, got %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d: %q != %q", i, a[i], b[i])
		}
	}
}

// Different seeds should (almost surely) give different results.
func TestSelectWinners_SeedSensitivity(t *testing.T) {
	p := pool(1000)
	a := SelectWinners(p, 10, []byte("seed-one"))
	b := SelectWinners(p, 10, []byte("seed-two"))
	same := true
	for i := range a {
		if a[i] != b[i] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seeds produced identical winners")
	}
}

// No entry wins twice.
func TestSelectWinners_Distinct(t *testing.T) {
	winners := SelectWinners(pool(500), 50, []byte("seed"))
	seen := map[string]bool{}
	for _, w := range winners {
		if seen[w] {
			t.Fatalf("duplicate winner %q", w)
		}
		seen[w] = true
	}
}

func TestSelectWinners_Edges(t *testing.T) {
	if r := SelectWinners(nil, 5, []byte("s")); r != nil {
		t.Fatal("empty pool should yield nil")
	}
	if r := SelectWinners(pool(3), 0, []byte("s")); r != nil {
		t.Fatal("zero count should yield nil")
	}
	if r := SelectWinners(pool(3), 10, []byte("s")); len(r) != 3 {
		t.Fatalf("count>pool should return whole pool, got %d", len(r))
	}
}

// Uniformity sanity: over many seeds, each entry should win roughly equally.
func TestSelectWinners_Uniform(t *testing.T) {
	const N = 50
	const trials = 20000
	counts := make([]int, N)
	p := pool(N)
	for i := 0; i < trials; i++ {
		w := SelectWinners(p, 1, []byte(fmt.Sprintf("seed-%d", i)))
		var idx int
		fmt.Sscanf(w[0], "entry-%d", &idx)
		counts[idx-1]++
	}
	expected := float64(trials) / float64(N)
	// Chi-square goodness-of-fit; with 49 dof, 95th percentile ~ 66.3.
	var chi float64
	for _, c := range counts {
		d := float64(c) - expected
		chi += d * d / expected
	}
	if chi > 90 {
		t.Fatalf("selection looks non-uniform: chi-square=%.1f (expected ~49)", chi)
	}
}

func TestAllocateInstantPrizes_Deterministic(t *testing.T) {
	units := []string{"cash:100", "cash:100", "phys:bike", "credit:5"}
	seed := []byte("instant seed")
	a := AllocateInstantPrizes(10000, units, seed)
	b := AllocateInstantPrizes(10000, units, seed)
	if len(a) != len(units) {
		t.Fatalf("expected %d placements, got %d", len(units), len(a))
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("non-deterministic allocation at ticket %d", k)
		}
	}
}

func TestAllocateInstantPrizes_DistinctTickets(t *testing.T) {
	units := make([]string, 200)
	for i := range units {
		units[i] = fmt.Sprintf("prize-%d", i)
	}
	alloc := AllocateInstantPrizes(5000, units, []byte("s"))
	if len(alloc) != 200 {
		t.Fatalf("expected 200 distinct placements, got %d", len(alloc))
	}
	for tn := range alloc {
		if tn < 1 || tn > 5000 {
			t.Fatalf("ticket number out of range: %d", tn)
		}
	}
}

func TestDigests_StableAndOrderIndependent(t *testing.T) {
	d1 := DigestStringsSorted([]string{"c", "a", "b"})
	d2 := DigestStringsSorted([]string{"b", "c", "a"})
	if d1 != d2 {
		t.Fatal("sorted digest should be order-independent")
	}
	o1 := DigestStringsOrdered([]string{"a", "b"})
	o2 := DigestStringsOrdered([]string{"b", "a"})
	if o1 == o2 {
		t.Fatal("ordered digest should depend on order")
	}
	// Framing prevents the classic concatenation collision ("a","bc") vs ("ab","c").
	c1 := DigestStringsOrdered([]string{"a", "bc"})
	c2 := DigestStringsOrdered([]string{"ab", "c"})
	if c1 == c2 {
		t.Fatal("length-framing should prevent concatenation collisions")
	}
}

func TestDigestAllocation_OrderIndependent(t *testing.T) {
	a := map[int]string{3: "x", 1: "y", 2: "z"}
	if DigestAllocation(a) != DigestAllocation(map[int]string{1: "y", 2: "z", 3: "x"}) {
		t.Fatal("allocation digest must not depend on map order")
	}
}

func TestMixExternalEntropy(t *testing.T) {
	local := []byte("local")
	beacon := []byte("beacon-round-42")
	mixed := MixExternalEntropy(local, beacon)
	if len(mixed) != 32 {
		t.Fatalf("mixed seed should be 32 bytes, got %d", len(mixed))
	}
	// Changing the beacon changes the seed.
	if string(mixed) == string(MixExternalEntropy(local, []byte("beacon-round-43"))) {
		t.Fatal("different beacon must change the mixed seed")
	}
}

// intn must not exhibit obvious modulo bias for non-power-of-two n.
func TestIntn_NoModuloBias(t *testing.T) {
	d := newDRBG([]byte("bias-test"))
	const n = 7
	const trials = 70000
	counts := make([]int, n)
	for i := 0; i < trials; i++ {
		counts[d.intn(n)]++
	}
	expected := float64(trials) / float64(n)
	for v, c := range counts {
		if math.Abs(float64(c)-expected)/expected > 0.05 {
			t.Fatalf("value %d appears biased: count=%d expected≈%.0f", v, c, expected)
		}
	}
}
