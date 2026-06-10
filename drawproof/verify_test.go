package drawproof

import (
	"encoding/hex"
	"testing"
)

// Full round-trip with a FUTURE pulse: produce a draw exactly as the platform
// would, then verify it — including the anti-grind check.
func TestVerifyMainDraw_RoundTrip(t *testing.T) {
	pool := pool(2000)
	local := []byte("platform-local-seed-component-xx")
	beaconVal := []byte("public-drand-beacon-randomness")
	final := MixExternalEntropy(local, beaconVal)

	winners := SelectWinners(CanonicalOrder(pool), 5, final)

	commit := CommitRecord{
		Kind:        KindMainDraw,
		RoundID:     7,
		InputDigest: DigestStringsSorted(pool),
		SeedHash:    HashSeed(local), // binds the LOCAL seed
		BeaconPulse: 1234,
		CommittedAt: "2026-06-08T10:00:00Z",
	}
	reveal := RevealRecord{
		Kind:            KindMainDraw,
		RoundID:         7,
		Seed:            hex.EncodeToString(final),
		LocalSeed:       hex.EncodeToString(local),
		BeaconPulse:     1234,
		BeaconValue:     hex.EncodeToString(beaconVal),
		BeaconPulseTime: "2026-06-08T10:05:00Z", // pulse AFTER the commit => anti-grind holds
		Winners:         winners,
		WinnerDigest:    DigestStringsOrdered(winners),
	}

	res := VerifyMainDraw(pool, commit, reveal)
	if !res.OK {
		for _, c := range res.Checks {
			if !c.OK {
				t.Errorf("check failed: %s (%s)", c.Name, c.Detail)
			}
		}
		t.Fatal("expected verification to pass")
	}

	// Confirm the future-pulse anti-grind check actually ran and passed.
	found := false
	for _, c := range res.Checks {
		if c.Name == "future-pulse-anti-grind" {
			found = true
			if !c.OK {
				t.Fatal("anti-grind check should pass for a future pulse")
			}
		}
	}
	if !found {
		t.Fatal("expected a future-pulse-anti-grind check")
	}
}

// A pulse scheduled BEFORE the commit fails the anti-grind check.
func TestVerifyMainDraw_PastPulseFailsAntiGrind(t *testing.T) {
	pool := pool(100)
	local := []byte("local")
	beaconVal := []byte("beacon")
	final := MixExternalEntropy(local, beaconVal)
	winners := SelectWinners(CanonicalOrder(pool), 2, final)

	commit := CommitRecord{
		InputDigest: DigestStringsSorted(pool),
		SeedHash:    HashSeed(local),
		BeaconPulse: 9,
		CommittedAt: "2026-06-08T10:00:00Z",
	}
	reveal := RevealRecord{
		Seed:            hex.EncodeToString(final),
		LocalSeed:       hex.EncodeToString(local),
		BeaconPulse:     9,
		BeaconValue:     hex.EncodeToString(beaconVal),
		BeaconPulseTime: "2026-06-08T09:55:00Z", // BEFORE commit => platform could have known it
		Winners:         winners,
		WinnerDigest:    DigestStringsOrdered(winners),
	}
	res := VerifyMainDraw(pool, commit, reveal)
	if res.OK {
		t.Fatal("a past pulse must fail the anti-grind check")
	}
}

func TestVerifyMainDraw_TamperedWinner(t *testing.T) {
	pool := pool(500)
	local := []byte("local")
	final := MixExternalEntropy(local, []byte("beacon"))
	winners := SelectWinners(CanonicalOrder(pool), 3, final)
	tampered := append([]string{}, winners...)
	tampered[0] = "entry-999999" // swap in a non-winner

	commit := CommitRecord{InputDigest: DigestStringsSorted(pool), SeedHash: HashSeed(local), BeaconPulse: 1}
	reveal := RevealRecord{
		Seed:         hex.EncodeToString(final),
		LocalSeed:    hex.EncodeToString(local),
		BeaconPulse:  1,
		BeaconValue:  hex.EncodeToString([]byte("beacon")),
		Winners:      tampered,
		WinnerDigest: DigestStringsOrdered(tampered),
	}
	res := VerifyMainDraw(pool, commit, reveal)
	if res.OK {
		t.Fatal("tampered winner should fail verification")
	}
}

func TestVerifyMainDraw_TamperedLocalSeed(t *testing.T) {
	pool := pool(500)
	local := []byte("the-real-local-seed")
	final := MixExternalEntropy(local, []byte("beacon"))
	winners := SelectWinners(CanonicalOrder(pool), 3, final)

	// Commit binds the real local seed, but the reveal discloses a different one.
	commit := CommitRecord{InputDigest: DigestStringsSorted(pool), SeedHash: HashSeed(local), BeaconPulse: 1}
	reveal := RevealRecord{
		Seed:         hex.EncodeToString(final),
		LocalSeed:    hex.EncodeToString([]byte("a-different-local-seed")),
		BeaconPulse:  1,
		BeaconValue:  hex.EncodeToString([]byte("beacon")),
		Winners:      winners,
		WinnerDigest: DigestStringsOrdered(winners),
	}
	res := VerifyMainDraw(pool, commit, reveal)
	if res.OK {
		t.Fatal("local seed not matching committed hash should fail")
	}
}

func TestVerifyMainDraw_LocalOnlySeed(t *testing.T) {
	// Degraded mode: no beacon. Final seed must equal the local seed.
	pool := pool(300)
	local := []byte("local-only-seed")
	winners := SelectWinners(CanonicalOrder(pool), 2, local)

	commit := CommitRecord{InputDigest: DigestStringsSorted(pool), SeedHash: HashSeed(local)}
	reveal := RevealRecord{
		Seed:         hex.EncodeToString(local),
		LocalSeed:    hex.EncodeToString(local),
		Winners:      winners,
		WinnerDigest: DigestStringsOrdered(winners),
	}
	if res := VerifyMainDraw(pool, commit, reveal); !res.OK {
		for _, c := range res.Checks {
			if !c.OK {
				t.Errorf("failed: %s (%s)", c.Name, c.Detail)
			}
		}
		t.Fatal("local-only seed draw should verify")
	}
}

// buildInstantAllocation mirrors what the platform does at mint: build the prize
// units as canonical references, allocate them to ticket numbers from the seed.
func buildInstantAllocation(totalTickets int, unitRefs []string, seed []byte) map[int]string {
	units := CanonicalOrder(unitRefs)
	return AllocateInstantPrizes(totalTickets, units, seed)
}

func TestVerifyInstantFromSeed_RoundTrip(t *testing.T) {
	const totalTickets = 10000
	// Three prize pools: 2x cash, 1x physical, 3x site-credit.
	unitRefs := []string{
		"pool=1;type=INSTANT_CASH;amount=100",
		"pool=1;type=INSTANT_CASH;amount=100",
		"pool=2;type=INSTANT_PHYSICAL;amount=nil",
		"pool=3;type=INSTANT_SITE_CREDIT;amount=5",
		"pool=3;type=INSTANT_SITE_CREDIT;amount=5",
		"pool=3;type=INSTANT_SITE_CREDIT;amount=5",
	}
	local := []byte("instant-local-seed")
	beaconVal := []byte("instant-beacon-value")
	final := MixExternalEntropy(local, beaconVal)

	alloc := buildInstantAllocation(totalTickets, unitRefs, final)
	if len(alloc) != len(unitRefs) {
		t.Fatalf("expected %d placements, got %d", len(unitRefs), len(alloc))
	}

	commit := CommitRecord{
		Kind:        KindInstant,
		RoundID:     5,
		InputDigest: DigestAllocation(alloc),
		SeedHash:    HashSeed(local),
		BeaconPulse: 808,
		CommittedAt: "2026-06-09T09:00:00Z",
	}
	reveal := RevealRecord{
		Kind:         KindInstant,
		RoundID:      5,
		Seed:         hex.EncodeToString(final),
		LocalSeed:    hex.EncodeToString(local),
		BeaconPulse:  808,
		BeaconValue:  hex.EncodeToString(beaconVal),
		TotalTickets: totalTickets,
		WinnerDigest: DigestAllocation(alloc),
	}

	res := VerifyInstantFromSeed(totalTickets, alloc, commit, reveal)
	if !res.OK {
		for _, c := range res.Checks {
			if !c.OK {
				t.Errorf("failed: %s (%s)", c.Name, c.Detail)
			}
		}
		t.Fatal("expected instant-from-seed verification to pass")
	}
}

func TestVerifyInstantFromSeed_TamperedAllocation(t *testing.T) {
	const totalTickets = 1000
	unitRefs := []string{"pool=1;type=INSTANT_CASH;amount=50", "pool=2;type=INSTANT_PHYSICAL;amount=nil"}
	local := []byte("local")
	final := MixExternalEntropy(local, []byte("beacon"))
	alloc := buildInstantAllocation(totalTickets, unitRefs, final)

	commit := CommitRecord{InputDigest: DigestAllocation(alloc), SeedHash: HashSeed(local), BeaconPulse: 1}
	reveal := RevealRecord{
		Seed: hex.EncodeToString(final), LocalSeed: hex.EncodeToString(local),
		BeaconPulse: 1, BeaconValue: hex.EncodeToString([]byte("beacon")), TotalTickets: totalTickets,
	}
	// Move a prize to a different ticket number: digest and reproduction both break.
	for tn, ref := range alloc {
		delete(alloc, tn)
		alloc[tn+500000] = ref
		break
	}
	if res := VerifyInstantFromSeed(totalTickets, alloc, commit, reveal); res.OK {
		t.Fatal("tampered allocation must fail seed reproduction")
	}
}

func TestVerifyInstant_RoundTrip(t *testing.T) {
	alloc := map[int]string{
		17:   "pool=1;type=INSTANT_CASH;amount=100",
		420:  "pool=2;type=INSTANT_PHYSICAL;amount=nil",
		1337: "pool=1;type=INSTANT_CASH;amount=100",
	}
	commit := CommitRecord{
		Kind:        KindInstant,
		RoundID:     3,
		InputDigest: DigestAllocation(alloc),
		CommittedAt: "2026-06-08T10:00:00Z",
	}
	if res := VerifyInstant(alloc, commit); !res.OK {
		t.Fatal("expected instant verification to pass")
	}

	// Tamper: move a prize to a different ticket number.
	alloc[18] = alloc[17]
	delete(alloc, 17)
	if res := VerifyInstant(alloc, commit); res.OK {
		t.Fatal("altered allocation should fail against the pre-sales commit")
	}
}
