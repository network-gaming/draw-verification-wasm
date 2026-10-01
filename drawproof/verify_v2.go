package drawproof

import (
	"encoding/hex"
	"fmt"
)

// VerifyInstantV2Sealed performs the checks possible on an Instant Wins v2 bundle
// whose seed has NOT yet been disclosed (the round is still on sale, or the
// bundle was exported before disclosure): the rules are bound to the commit and
// the commit is well-formed. The allocation itself cannot be checked yet.
func VerifyInstantV2Sealed(b Bundle) VerifyResult {
	res := VerifyResult{OK: true}
	res.add("algorithm-version", b.Commit.AlgorithmVersion == AlgorithmVersionV2,
		fmt.Sprintf("commit declares algorithm version %d", b.Commit.AlgorithmVersion))
	verifyRulesDigest(&res, b)
	res.add("population-committed", b.Commit.TotalTickets > 0,
		fmt.Sprintf("%d tickets committed before sales", b.Commit.TotalTickets))
	res.add("seed-sealed", b.Reveal.Seed == "", "seed not yet disclosed; allocation cannot be reproduced until round close")
	return res
}

// VerifyInstantV2FromSeed is the full Instant Wins v2 verification. Given the
// disclosed allocation (salePosition -> prize reference), the canonical rules,
// the pre-sales commit and the post-close reveal, it confirms in order:
//
//  1. the rules digest matches the commit
//  2. the allocation digest matches the commit
//  3. the committed hash binds the disclosed local seed
//  4. the beacon mix reproduces the final seed
//  5. the allocation is reproduced from (M, units, rules, seed) in exactly the
//     recorded number of attempts
//  6. every placed unit satisfies every rule that matches it
//  7. the display permutation reproduced from the seed matches the committed
//     digest (and the published ticket numbers, when included)
func VerifyInstantV2FromSeed(b Bundle) VerifyResult {
	res := VerifyResult{OK: true}
	commit, reveal := b.Commit, b.Reveal
	allocation := parseIntKeyMap(b.Allocation)
	// A bundle may publish the allocation as unit counts instead of positions
	// (see Bundle.Units); then the allocation is the reproduction itself and
	// its digest is checked once it exists, in step 5.
	fromCounts := len(allocation) == 0 && len(b.Units) > 0
	totalTickets := commit.TotalTickets
	if totalTickets == 0 {
		totalTickets = reveal.TotalTickets
	}

	res.add("algorithm-version", commit.AlgorithmVersion == AlgorithmVersionV2,
		fmt.Sprintf("commit declares algorithm version %d", commit.AlgorithmVersion))

	// 1. Rules bound to the commit.
	rules, rulesOK := verifyRulesDigest(&res, b)

	// 2. Allocation bound to the commit.
	if !fromCounts {
		res.add("allocation-digest-matches-commit", DigestAllocation(allocation) == commit.InputDigest,
			fmt.Sprintf("committed %s before sales at %s", short(commit.InputDigest), commit.CommittedAt))
	}

	// 3. Local seed bound to the commit.
	local, err := hex.DecodeString(reveal.LocalSeed)
	if err != nil {
		res.add("local-seed-decodes", false, err.Error())
		return res
	}
	res.add("local-seed-hash-matches-commit", HashSeed(local) == commit.SeedHash,
		fmt.Sprintf("committed %s", short(commit.SeedHash)))

	// 4. Beacon mix.
	if reveal.BeaconValue != "" {
		bv, err := hex.DecodeString(reveal.BeaconValue)
		if err != nil {
			res.add("beacon-mix", false, "could not decode beacon value")
		} else {
			res.add("beacon-mix", hex.EncodeToString(MixExternalEntropy(local, bv)) == reveal.Seed,
				fmt.Sprintf("beacon pulse %d folded into seed", reveal.BeaconPulse))
		}
		res.add("beacon-pulse-matches-commit",
			commit.BeaconPulse != 0 && commit.BeaconPulse == reveal.BeaconPulse,
			fmt.Sprintf("committed pulse %d", commit.BeaconPulse))
	} else {
		res.add("beacon-mix", hex.EncodeToString(local) == reveal.Seed,
			"no external beacon disclosed (platform-trusted local seed)")
	}

	finalSeed, err := hex.DecodeString(reveal.Seed)
	if err != nil {
		res.add("seed-decodes", false, err.Error())
		return res
	}
	if !rulesOK {
		res.add("allocation-reproduced-from-seed", false, "cannot reproduce without valid rules")
		return res
	}

	// 5. Reproduce the placement, in the compact form: the disclosed map is
	// one entry per prize, the Placement one small integer per position.
	var (
		disclosed *Placement
		counts    UnitCounts
	)
	if fromCounts {
		counts = UnitCounts(b.Units)
	} else {
		disclosed, err = PlacementFromMap(totalTickets, allocation)
		if err != nil {
			res.add("allocation-reproduced-from-seed", false, err.Error())
			return res
		}
		counts = disclosed.Counts()
	}
	wantAttempts := reveal.Attempts
	if wantAttempts == 0 {
		wantAttempts = 1
	}
	reproduced, attempts, err := AllocatePlacement(totalTickets, counts, rules, finalSeed)
	switch {
	case err != nil:
		res.add("allocation-reproduced-from-seed", false, err.Error())
		if fromCounts {
			res.add("allocation-digest-matches-commit", false, "allocation could not be reproduced")
		}
	case fromCounts:
		// Published as counts: the reproduction is the allocation, bound to
		// the commit (and to the reveal, when it names a digest) by digest.
		digest := reproduced.Digest()
		res.add("allocation-digest-matches-commit", digest == commit.InputDigest,
			fmt.Sprintf("%d prizes reproduced from %d unit count(s); committed %s before sales at %s",
				reproduced.Len(), len(b.Units), short(commit.InputDigest), commit.CommittedAt))
		revealOK := reveal.WinnerDigest == "" || reveal.WinnerDigest == digest
		res.add("allocation-reproduced-from-seed", revealOK && attempts == wantAttempts,
			fmt.Sprintf("%d prizes re-derived over %d positions in %d attempt(s)", reproduced.Len(), totalTickets, attempts))
		disclosed = reproduced
	default:
		res.add("allocation-reproduced-from-seed", reproduced.Equal(disclosed) && attempts == wantAttempts,
			fmt.Sprintf("%d prizes re-derived over %d positions in %d attempt(s)", len(allocation), totalTickets, attempts))
	}

	// 6. Every rule holds on the disclosed (or, from counts, reproduced) allocation.
	if err := disclosed.CheckRules(rules); err != nil {
		res.add("rules-satisfied", false, err.Error())
	} else {
		res.add("rules-satisfied", true, fmt.Sprintf("%d rule(s) hold for every placed prize", len(rules.Rules)))
	}

	// 7. Display permutation.
	perm := DisplayPermutation(totalTickets, finalSeed)
	res.add("display-permutation-matches-commit", DigestPermutation(perm) == commit.DisplayDigest,
		fmt.Sprintf("committed %s", short(commit.DisplayDigest)))
	if len(b.TicketNumbers) > 0 {
		same := len(b.TicketNumbers) == len(perm)
		for i := 0; same && i < len(perm); i++ {
			if b.TicketNumbers[i] != perm[i] {
				same = false
			}
		}
		res.add("ticket-numbers-match-permutation", same,
			fmt.Sprintf("%d published ticket numbers match the seeded permutation", len(b.TicketNumbers)))
	}

	return res
}

// verifyRulesDigest parses the bundle's rules, checks them against the commit,
// and records the result. Returns the parsed rules and whether they are usable.
func verifyRulesDigest(res *VerifyResult, b Bundle) (InstantRules, bool) {
	rules, err := ParseRules(b.Rules)
	if err != nil {
		res.add("rules-digest-matches-commit", false, "rules could not be parsed: "+err.Error())
		return rules, false
	}
	_, digest, err := CanonicalRules(rules)
	if err != nil {
		res.add("rules-digest-matches-commit", false, err.Error())
		return rules, false
	}
	ok := digest == b.Commit.RulesDigest
	res.add("rules-digest-matches-commit", ok,
		fmt.Sprintf("%d rule(s), committed %s", len(rules.Rules), short(b.Commit.RulesDigest)))
	return rules, ok
}
