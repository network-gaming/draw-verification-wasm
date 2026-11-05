package drawproof

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// Check is the result of a single verification step.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// VerifyResult aggregates the checks for a draw. Recorded is set for a
// MANUAL main draw: the checks that ran prove the pool was frozen and the
// winners were in it, not that the outcome is reproducible, so OK on a
// recorded result must never be presented as "verified".
type VerifyResult struct {
	OK       bool    `json:"ok"`
	Checks   []Check `json:"checks"`
	Recorded bool    `json:"recorded,omitempty"`
}

func (r *VerifyResult) add(name string, ok bool, detail string) {
	if !ok {
		r.OK = false
	}
	r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: detail})
}

// VerifyMainDraw independently re-derives a main/vault prize draw from its
// published inputs and confirms it matches the committed and revealed records.
//
//   - pool: the frozen, published eligible-entry identifiers.
//   - commit: the pre-draw commitment (input digest, local-seed hash, beacon pulse).
//   - reveal: the post-draw disclosure (local seed, final seed, beacon value, winners).
//
// It verifies the local-seed commitment, the beacon mix, the pulse binding, the
// pool digest, and reproduces the winners deterministically. When the named pulse
// was scheduled AFTER the commit (a future pulse), it additionally confirms the
// platform could not have known the beacon value when committing. The result OK
// is true only if every check passes.
func VerifyMainDraw(pool []string, commit CommitRecord, reveal RevealRecord) VerifyResult {
	sorted := CanonicalOrder(pool)
	return VerifyMainDrawIndexed(len(sorted), func(i int) string { return sorted[i] }, commit, reveal)
}

// VerifyMainDrawIndexed is VerifyMainDraw over a pool that is not held as a
// []string: at(i) returns the i-th entry of the pool in canonical (byte-wise
// ascending) order, for i in [0, n). The digest is computed as a stream and
// the winners are resolved by rank (SelectWinnerRanks), so a ten-million-entry
// pool costs the caller only its compact representation. The caller must
// supply the entries sorted; an unsorted pool fails the digest check.
func VerifyMainDrawIndexed(n int, at func(i int) string, commit CommitRecord, reveal RevealRecord) VerifyResult {
	if commit.Method.IsManual() || reveal.Method.IsManual() {
		return verifyRecordedMainDraw(n, at, commit, reveal)
	}
	res := VerifyResult{OK: true}

	finalSeed, err := hex.DecodeString(reveal.Seed)
	if err != nil {
		res.add("seed-decodes", false, err.Error())
		return res
	}
	local, err := hex.DecodeString(reveal.LocalSeed)
	if err != nil {
		res.add("local-seed-decodes", false, err.Error())
		return res
	}

	// 1. The hash committed before the draw binds the platform's local seed.
	res.add("local-seed-hash-matches-commit", HashSeed(local) == commit.SeedHash,
		fmt.Sprintf("committed %s", short(commit.SeedHash)))

	// 2. Beacon mix: final seed = SHA256(localSeed || beaconValue), or identity
	//    when no beacon was used (degraded, platform-trusted seed).
	if reveal.BeaconValue != "" {
		bv, err := hex.DecodeString(reveal.BeaconValue)
		if err != nil {
			res.add("beacon-mix", false, "could not decode beacon value")
		} else {
			ok := hex.EncodeToString(MixExternalEntropy(local, bv)) == reveal.Seed
			res.add("beacon-mix", ok, fmt.Sprintf("beacon pulse %d folded into seed", reveal.BeaconPulse))
		}
		// 2a. The pulse used must be the pulse named in the commitment.
		res.add("beacon-pulse-matches-commit",
			commit.BeaconPulse != 0 && commit.BeaconPulse == reveal.BeaconPulse,
			fmt.Sprintf("committed pulse %d", commit.BeaconPulse))
		// 2b. Future-pulse anti-grind: if the pulse was scheduled after the
		//     commit, the platform could not have known its value when committing.
		if reveal.BeaconPulseTime != "" && commit.CommittedAt != "" {
			pulseT, e1 := time.Parse(time.RFC3339, reveal.BeaconPulseTime)
			commitT, e2 := time.Parse(time.RFC3339, commit.CommittedAt)
			if e1 == nil && e2 == nil {
				future := pulseT.After(commitT)
				res.add("future-pulse-anti-grind", future,
					fmt.Sprintf("pulse published %s, committed %s",
						pulseT.UTC().Format(time.RFC3339), commitT.UTC().Format(time.RFC3339)))
			}
		}
	} else {
		// No beacon: the final seed must equal the committed local seed.
		res.add("beacon-mix", hex.EncodeToString(local) == reveal.Seed,
			"no external beacon disclosed (platform-trusted local seed)")
	}

	// 3. The published pool matches the digest committed before the draw. The
	//    digest is DigestStringsSorted computed as a stream over the sorted
	//    entries: an out-of-order entry is reported as such.
	poolDigest, sorted := poolDigestIndexed(n, at)
	if !sorted {
		res.add("pool-digest-matches-commit", false, "pool entries are not in canonical (byte-wise) order")
	} else {
		res.add("pool-digest-matches-commit", poolDigest == commit.InputDigest,
			fmt.Sprintf("committed %s", short(commit.InputDigest)))
	}

	// 4. Reproduce the winners deterministically and compare: the winners are
	//    the entries at the ranks the seed selects from the canonical pool.
	ranks := SelectWinnerRanks(n, len(reveal.Winners), finalSeed)
	match := sorted && len(ranks) == len(reveal.Winners)
	for i := 0; match && i < len(ranks); i++ {
		if at(ranks[i]) != reveal.Winners[i] {
			match = false
		}
	}
	res.add("winners-reproduced", match,
		fmt.Sprintf("%d winners reproduced from (pool, seed)", len(reveal.Winners)))

	// 5. Winner digest is internally consistent.
	res.add("winner-digest-consistent", DigestStringsOrdered(reveal.Winners) == reveal.WinnerDigest, "")

	return res
}

// poolDigestIndexed streams the pool through the sorted digest and reports
// whether the entries were in canonical (byte-wise ascending) order.
func poolDigestIndexed(n int, at func(i int) string) (digest string, sorted bool) {
	h := sha256.New()
	sorted = true
	prev := ""
	for i := 0; i < n; i++ {
		s := at(i)
		if i > 0 && s < prev {
			sorted = false
		}
		writeFramed(h, s)
		prev = s
	}
	return hex.EncodeToString(h.Sum(nil)), sorted
}

// verifyRecordedMainDraw is VerifyMainDrawIndexed for a MANUAL (recorded)
// main draw. There is no seed to replay, so the checks are the ones a
// recorded outcome can still answer: the commitment said in advance that the
// draw would be recorded, the pool published is the pool frozen before the
// draw, every recorded winner was an entry in that pool, no entry was
// recorded twice, and the winners list was not edited after the record. The
// result is marked Recorded; it says nothing about how the winners were
// chosen.
func verifyRecordedMainDraw(n int, at func(i int) string, commit CommitRecord, reveal RevealRecord) VerifyResult {
	res := VerifyResult{OK: true, Recorded: true}

	// 0. Informational: what this result does and does not prove.
	res.add("recorded-draw", true,
		"the winners were drawn by hand and recorded by the operator; the outcome is not reproducible from a seed")

	// 1. The commitment, written before the draw, already said the draw would
	//    be recorded, and the reveal agrees: a computed draw cannot be
	//    re-labelled as recorded after the fact, nor the other way round.
	res.add("method-matches-commit", commit.Method.IsManual() && reveal.Method.IsManual(),
		fmt.Sprintf("committed %q, revealed %q", commit.Method, reveal.Method))

	// 2. The published pool matches the digest committed before the draw.
	poolDigest, sorted := poolDigestIndexed(n, at)
	if !sorted {
		res.add("pool-digest-matches-commit", false, "pool entries are not in canonical (byte-wise) order")
	} else {
		res.add("pool-digest-matches-commit", poolDigest == commit.InputDigest,
			fmt.Sprintf("committed %s", short(commit.InputDigest)))
	}

	// 3. Every recorded winner is an entry of the frozen pool. The pool is in
	//    canonical order, so each winner is found by binary search.
	missing := 0
	for _, w := range reveal.Winners {
		if !sorted || !poolContains(n, at, w) {
			missing++
		}
	}
	res.add("winners-in-pool", sorted && missing == 0,
		fmt.Sprintf("%d of %d recorded winners found in the frozen pool", len(reveal.Winners)-missing, len(reveal.Winners)))

	// 4. No entry was recorded as a winner twice: a drawn slip leaves the box.
	seen := make(map[string]bool, len(reveal.Winners))
	dup := 0
	for _, w := range reveal.Winners {
		if seen[w] {
			dup++
		}
		seen[w] = true
	}
	res.add("winners-distinct", dup == 0, fmt.Sprintf("%d duplicate winners", dup))

	// 5. Winner digest is internally consistent.
	res.add("winner-digest-consistent", DigestStringsOrdered(reveal.Winners) == reveal.WinnerDigest, "")

	return res
}

// poolContains reports whether entry is in the canonically ordered pool.
func poolContains(n int, at func(i int) string, entry string) bool {
	lo, hi := 0, n
	for lo < hi {
		mid := lo + (hi-lo)/2
		if at(mid) < entry {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < n && at(lo) == entry
}

// VerifyInstant confirms that a disclosed instant-prize allocation matches the
// digest that was committed BEFORE sales opened — proving the complete set of
// winning ticket numbers was sealed in advance and unaltered.
//
//   - allocation: the disclosed ticketNumber -> prize-reference map.
//   - commit: the pre-sales commitment.
func VerifyInstant(allocation map[int]string, commit CommitRecord) VerifyResult {
	res := VerifyResult{OK: true}
	digest := DigestAllocation(allocation)
	res.add("allocation-digest-matches-commit", digest == commit.InputDigest,
		fmt.Sprintf("committed %s before sales at %s", short(commit.InputDigest), commit.CommittedAt))
	res.add("sealed-before-sales", commit.CommittedAt != "",
		"commit is a verified, timestamped ledger record written at mint")
	return res
}

// VerifyInstantFromSeed performs the strongest instant-win check: it re-derives
// the entire ticket-number -> prize allocation from the disclosed seed using the
// published algorithm and confirms it reproduces the sealed allocation exactly.
// This proves the allocation is a well-formed deterministic output of the seed
// committed before sales — not merely that its digest matches.
//
//   - totalTickets: the ticket population size (reveal.TotalTickets).
//   - allocation: the disclosed ticketNumber -> prize-reference map.
//   - commit: the pre-sales commitment.
//   - reveal: the disclosure (local seed, final seed, beacon value).
//
// The prize-unit list is reconstructed from the allocation's own values (the
// multiset of placed prizes) in canonical order — the same order the platform
// uses — so no additional configuration needs to be published.
func VerifyInstantFromSeed(totalTickets int, allocation map[int]string, commit CommitRecord, reveal RevealRecord) VerifyResult {
	res := VerifyResult{OK: true}

	// 1. The disclosed allocation matches the digest committed before sales.
	res.add("allocation-digest-matches-commit", DigestAllocation(allocation) == commit.InputDigest,
		fmt.Sprintf("committed %s before sales at %s", short(commit.InputDigest), commit.CommittedAt))

	// 2. The committed hash binds the disclosed local seed.
	local, err := hex.DecodeString(reveal.LocalSeed)
	if err != nil {
		res.add("local-seed-decodes", false, err.Error())
		return res
	}
	res.add("local-seed-hash-matches-commit", HashSeed(local) == commit.SeedHash,
		fmt.Sprintf("committed %s", short(commit.SeedHash)))

	// 3. Beacon mix reproduces the final seed (or identity when no beacon).
	if reveal.BeaconValue != "" {
		bv, err := hex.DecodeString(reveal.BeaconValue)
		if err != nil {
			res.add("beacon-mix", false, "could not decode beacon value")
		} else {
			res.add("beacon-mix", hex.EncodeToString(MixExternalEntropy(local, bv)) == reveal.Seed,
				fmt.Sprintf("beacon pulse %d folded into seed", reveal.BeaconPulse))
		}
	} else {
		res.add("beacon-mix", hex.EncodeToString(local) == reveal.Seed,
			"no external beacon disclosed (platform-trusted local seed)")
	}

	// 4. Reproduce the allocation from the seed and confirm it matches.
	finalSeed, err := hex.DecodeString(reveal.Seed)
	if err != nil {
		res.add("seed-decodes", false, err.Error())
		return res
	}
	units := make([]string, 0, len(allocation))
	for _, ref := range allocation {
		units = append(units, ref)
	}
	units = CanonicalOrder(units)
	reproduced := AllocateInstantPrizes(totalTickets, units, finalSeed)
	res.add("allocation-reproduced-from-seed", allocationsEqual(reproduced, allocation),
		fmt.Sprintf("%d prizes re-derived from seed over %d tickets", len(allocation), totalTickets))

	return res
}

func allocationsEqual(a, b map[int]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12] + "…"
}
