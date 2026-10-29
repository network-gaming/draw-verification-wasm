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
	verifyMintRecord(&res, b)
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
//
// On a PUBLIC bundle, which withholds the rules (Bundle.Rules empty, the
// commit's RulesDigest set), steps 1, 5 and 6 cannot run: step 1 records
// `rules-withheld` instead, and the verdict rests on steps 2, 3, 4 and 7. That
// proves the published placement is the one sealed before the first sale and
// that the ticket numbering derives from the disclosed seed; whether the
// placement honours the rules is established on the audit bundle.
func VerifyInstantV2FromSeed(b Bundle) VerifyResult {
	allocation := parseIntKeyMap(b.Allocation)
	if len(allocation) == 0 {
		return verifyInstantV2(b, nil)
	}
	disclosed, err := PlacementFromMap(totalTicketsOf(b), allocation)
	if err != nil {
		res := VerifyResult{OK: true}
		res.add("algorithm-version", b.Commit.AlgorithmVersion == AlgorithmVersionV2,
			fmt.Sprintf("commit declares algorithm version %d", b.Commit.AlgorithmVersion))
		res.add("allocation-digest-matches-commit", false, err.Error())
		return res
	}
	return verifyInstantV2(b, disclosed)
}

// VerifyInstantV2Placement is VerifyInstantV2FromSeed over a placement loaded
// from the bundle's AllocationFile (through an AllocationFeed) rather than
// its inline Allocation. A nil placement falls back to the inline forms.
func VerifyInstantV2Placement(b Bundle, disclosed *Placement) VerifyResult {
	if disclosed == nil {
		return VerifyInstantV2FromSeed(b)
	}
	return verifyInstantV2(b, disclosed)
}

func totalTicketsOf(b Bundle) int {
	if b.Commit.TotalTickets != 0 {
		return b.Commit.TotalTickets
	}
	return b.Reveal.TotalTickets
}

// verifyInstantV2 runs the v2 checks with the disclosed placement, or nil
// when the bundle publishes unit counts instead (see Bundle.Units); then the
// allocation is the reproduction itself and its digest is checked once it
// exists, in step 5.
func verifyInstantV2(b Bundle, disclosed *Placement) VerifyResult {
	res := VerifyResult{OK: true}
	commit, reveal := b.Commit, b.Reveal
	fromCounts := disclosed == nil
	totalTickets := totalTicketsOf(b)

	res.add("algorithm-version", commit.AlgorithmVersion == AlgorithmVersionV2,
		fmt.Sprintf("commit declares algorithm version %d", commit.AlgorithmVersion))

	// 1. Rules bound to the commit, or withheld.
	rules, rulesOK, withheld := verifyRulesDigest(&res, b)

	// 2. Allocation bound to the commit.
	if !fromCounts {
		res.add("allocation-digest-matches-commit", disclosed.Digest() == commit.InputDigest,
			fmt.Sprintf("%d prizes over %d positions; committed %s before sales at %s",
				disclosed.Len(), totalTickets, short(commit.InputDigest), commit.CommittedAt))
	} else if withheld {
		res.add("allocation-digest-matches-commit", false,
			"placement not published: a bundle that withholds the rules must publish the placement itself")
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
	if withheld {
		// 5 and 6 need the rules. The placement's binding to the commitment
		// (step 2) and the numbering (step 7) stand on their own.
		verifyDisplayPermutation(&res, b, totalTickets, finalSeed)
		verifyMintRecord(&res, b)
		return res
	}
	if !rulesOK {
		res.add("allocation-reproduced-from-seed", false, "cannot reproduce without valid rules")
		return res
	}

	// 5. Reproduce the placement, in the compact form: one small integer per
	// position.
	var counts UnitCounts
	if fromCounts {
		counts = UnitCounts(b.Units)
	} else {
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
			fmt.Sprintf("%d prizes re-derived over %d positions in %d attempt(s)", disclosed.Len(), totalTickets, attempts))
	}

	// 6. Every rule holds on the disclosed (or, from counts, reproduced) allocation.
	if err := disclosed.CheckRules(rules); err != nil {
		res.add("rules-satisfied", false, err.Error())
	} else {
		res.add("rules-satisfied", true, fmt.Sprintf("%d rule(s) hold for every placed prize", len(rules.Rules)))
	}

	// 7. Display permutation.
	verifyDisplayPermutation(&res, b, totalTickets, finalSeed)

	// 8. The platform's pre-sales read-back of the minted rows, when published.
	verifyMintRecord(&res, b)

	return res
}

// verifyDisplayPermutation reproduces the ticket numbering from the seed and
// checks it against the committed digest and, when published, the bundle's
// ticket numbers.
func verifyDisplayPermutation(res *VerifyResult, b Bundle, totalTickets int, finalSeed []byte) {
	perm := DisplayPermutation(totalTickets, finalSeed)
	res.add("display-permutation-matches-commit", DigestPermutation(perm) == b.Commit.DisplayDigest,
		fmt.Sprintf("committed %s", short(b.Commit.DisplayDigest)))
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
}

// verifyMintRecord checks a bundle's mint verification record, when it has
// one, against the commit: the rows that were read back before sales carried
// the committed allocation and display digests over the committed number of
// tickets, and the walk succeeded. A bundle without a record is not failed;
// the record is evidence of when the rows were first checked, not of the
// outcome, which the reproduction above establishes on its own.
func verifyMintRecord(res *VerifyResult, b Bundle) {
	v := b.Verify
	if v == nil {
		return
	}
	switch {
	case v.Outcome != VerifyOutcomeVerified:
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("mint verification outcome %q: %s", v.Outcome, v.Error))
	case v.Kind != b.Commit.Kind || v.RoundID != b.Commit.RoundID:
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("verification record is for %s round %d, bundle is %s round %d", v.Kind, v.RoundID, b.Commit.Kind, b.Commit.RoundID))
	case v.InputDigest != b.Commit.InputDigest:
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("rows read back carried allocation %s, committed %s", short(v.InputDigest), short(b.Commit.InputDigest)))
	case b.Commit.DisplayDigest != "" && v.DisplayDigest != b.Commit.DisplayDigest:
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("rows read back carried ticket numbers %s, committed %s", short(v.DisplayDigest), short(b.Commit.DisplayDigest)))
	case b.Commit.TotalTickets != 0 && (v.TotalTickets != b.Commit.TotalTickets || v.PositionsWalked != b.Commit.TotalTickets):
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("verification walked %d of %d rows, %d committed", v.PositionsWalked, v.TotalTickets, b.Commit.TotalTickets))
	case b.Commit.RulesDigest != "" && v.RulesDigest != "" && v.RulesDigest != b.Commit.RulesDigest:
		res.add("mint-verified-before-sales", false,
			fmt.Sprintf("verification saw rules %s, committed %s", short(v.RulesDigest), short(b.Commit.RulesDigest)))
	default:
		res.add("mint-verified-before-sales", true,
			fmt.Sprintf("%d rows read back by %s at %s matched the commitment", v.PositionsWalked, v.Verifier, v.FinishedAt))
	}
}

// verifyRulesDigest parses the bundle's rules, checks them against the commit,
// and records the result. Returns the parsed rules, whether they are usable,
// and whether the bundle withholds them: no rules published while the commit
// names a rules digest (a public bundle). A withheld rule set is recorded as
// `rules-withheld`, which passes: it is a statement about the bundle, not a
// finding, and the digest it names is checked on the audit bundle.
func verifyRulesDigest(res *VerifyResult, b Bundle) (rules InstantRules, ok, withheld bool) {
	if b.Rules == "" && b.Commit.RulesDigest != "" {
		res.add("rules-withheld", true,
			fmt.Sprintf("placement rules not published in this bundle; committed as %s and checked on the audit bundle", short(b.Commit.RulesDigest)))
		return InstantRules{}, false, true
	}
	rules, err := ParseRules(b.Rules)
	if err != nil {
		res.add("rules-digest-matches-commit", false, "rules could not be parsed: "+err.Error())
		return rules, false, false
	}
	_, digest, err := CanonicalRules(rules)
	if err != nil {
		res.add("rules-digest-matches-commit", false, err.Error())
		return rules, false, false
	}
	ok = digest == b.Commit.RulesDigest
	res.add("rules-digest-matches-commit", ok,
		fmt.Sprintf("%d rule(s), committed %s", len(rules.Rules), short(b.Commit.RulesDigest)))
	return rules, ok, false
}
