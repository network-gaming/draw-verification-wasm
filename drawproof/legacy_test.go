package drawproof

// Legacy (v0.3.0) implementations, copied verbatim from rules.go at tag v0.3.0
// and renamed, so the interval/rank implementation can be proven to reproduce
// them placement for placement. Only tests use them.

import (
	"fmt"
	"sort"
)

// legacyAllowedSet computes the positions (1-based, index 0 unused) a unit may occupy
// after applying every WINDOW and EXCLUDE rule that matches it.
func legacyAllowedSet(totalTickets int, ref string, rules InstantRules) []bool {
	allowed := make([]bool, totalTickets+1)
	for p := 1; p <= totalTickets; p++ {
		allowed[p] = true
	}
	for _, pr := range rules.Rules {
		if !pr.Matches(ref) {
			continue
		}
		switch pr.Type {
		case RuleWindow:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			for p := 1; p <= totalTickets; p++ {
				if p < lo || p > hi {
					allowed[p] = false
				}
			}
		case RuleExclude:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			for p := lo; p <= hi; p++ {
				allowed[p] = false
			}
		}
	}
	return allowed
}

type legacyUnitGroup struct {
	ref     string
	count   int
	allowed []bool
	size    int // number of allowed positions
	segment int // QUOTA sub-group bracket (0-based), -1 otherwise
}

// legacyBuildGroups groups units by reference, computes allowed sets, and orders the
// groups most-constrained first (smallest allowed set, then by reference).
func legacyBuildGroups(totalTickets int, units []string, rules InstantRules) []*legacyUnitGroup {
	byRef := map[string]*legacyUnitGroup{}
	var order []string
	for _, u := range units {
		g, ok := byRef[u]
		if !ok {
			g = &legacyUnitGroup{ref: u}
			byRef[u] = g
			order = append(order, u)
		}
		g.count++
	}
	groups := make([]*legacyUnitGroup, 0, len(order))
	for _, ref := range order {
		g := byRef[ref]
		g.segment = -1
		base := legacyAllowedSet(totalTickets, ref, rules)
		q, _ := quotaFor(ref, rules)
		if q == nil {
			g.allowed = base
			for p := 1; p <= totalTickets; p++ {
				if g.allowed[p] {
					g.size++
				}
			}
			groups = append(groups, g)
			continue
		}
		// One sub-group per bracket with a non-zero quota, restricted to that
		// bracket's positions (intersected with any window/exclusion).
		for k := 0; k < q.Segments; k++ {
			if q.Counts[k] == 0 {
				continue
			}
			lo, hi := QuotaSegmentBounds(totalTickets, q.Segments, k)
			sg := &legacyUnitGroup{ref: ref, count: q.Counts[k], segment: k, allowed: make([]bool, totalTickets+1)}
			for p := lo; p <= hi; p++ {
				if base[p] {
					sg.allowed[p] = true
					sg.size++
				}
			}
			groups = append(groups, sg)
		}
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].size != groups[j].size {
			return groups[i].size < groups[j].size
		}
		if groups[i].ref != groups[j].ref {
			return groups[i].ref < groups[j].ref
		}
		return groups[i].segment < groups[j].segment
	})
	return groups
}

// legacyCheckFeasibility verifies, without consuming any randomness, that every unit
// can be placed whatever the seed turns out to be. For direct-placement rules it
// uses a worst-case bound: every earlier (more constrained) group may consume
// positions inside a later group's allowed set. For rejection rules it checks
func legacyCheckFeasibility(totalTickets int, units []string, rules InstantRules) error {
	if err := rules.Validate(); err != nil {
		return err
	}
	if totalTickets <= 0 {
		return fmt.Errorf("%w: totalTickets must be > 0", ErrInfeasible)
	}
	if len(units) > totalTickets {
		return fmt.Errorf("%w: %d prize units exceed %d tickets", ErrInfeasible, len(units), totalTickets)
	}
	// QUOTA: exactly one quota per unit reference, and its counts must add up to
	// the units of that reference.
	perRef := map[string]int{}
	for _, u := range units {
		perRef[u]++
	}
	for ref, n := range perRef {
		q, matches := quotaFor(ref, rules)
		if matches > 1 {
			return fmt.Errorf("%w: %q matches %d QUOTA rules; at most one is allowed", ErrInfeasible, ref, matches)
		}
		if q == nil {
			continue
		}
		sum := 0
		for _, c := range q.Counts {
			sum += c
		}
		if sum != n {
			return fmt.Errorf("%w: QUOTA for %q places %d units but %d are issued", ErrInfeasible, ref, sum, n)
		}
	}
	groups := legacyBuildGroups(totalTickets, units, rules)
	for i, g := range groups {
		if g.size == 0 {
			if g.segment >= 0 {
				return fmt.Errorf("%w: %q has no allowed positions in bracket %d", ErrInfeasible, g.ref, g.segment+1)
			}
			return fmt.Errorf("%w: %q has no allowed positions", ErrInfeasible, g.ref)
		}
		consumed := 0
		for _, e := range groups[:i] {
			overlap := 0
			for p := 1; p <= totalTickets; p++ {
				if g.allowed[p] && e.allowed[p] {
					overlap++
				}
			}
			if overlap < e.count {
				consumed += overlap
			} else {
				consumed += e.count
			}
		}
		if g.size-consumed < g.count {
			where := "in its window"
			if g.segment >= 0 {
				where = fmt.Sprintf("in bracket %d", g.segment+1)
			}
			return fmt.Errorf("%w: %q needs %d positions but only %d can be guaranteed free %s",
				ErrInfeasible, g.ref, g.count, g.size-consumed, where)
		}
	}
	for _, pr := range rules.Rules {
		n := 0
		for _, g := range groups {
			if pr.Matches(g.ref) {
				n += g.count
			}
		}
		switch pr.Type {
		case RuleMinGap:
			if n > 1 && (n-1)*pr.Gap+1 > totalTickets {
				return fmt.Errorf("%w: %d units with gap %d cannot fit in %d tickets", ErrInfeasible, n, pr.Gap, totalTickets)
			}
		case RuleDensity:
			segLen := SegmentLength(totalTickets, pr.SegmentFraction)
			segments := (totalTickets + segLen - 1) / segLen
			if n > segments*pr.MaxUnits {
				return fmt.Errorf("%w: %d units exceed %d segments x %d maxUnits", ErrInfeasible, n, segments, pr.MaxUnits)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Placement
// ---------------------------------------------------------------------------

// legacyPlaceOnce performs one direct placement pass using the given DRBG.
func legacyPlaceOnce(totalTickets int, groups []*legacyUnitGroup, d *drbg) map[int]string {
	taken := make([]bool, totalTickets+1)
	out := make(map[int]string)
	for _, g := range groups {
		free := make([]int, 0, g.size)
		for p := 1; p <= totalTickets; p++ {
			if g.allowed[p] && !taken[p] {
				free = append(free, p)
			}
		}
		// Partial Fisher–Yates over the free positions, ascending order as the
		// canonical starting arrangement.
		for i := 0; i < g.count && i < len(free); i++ {
			j := i + d.intn(len(free)-i)
			free[i], free[j] = free[j], free[i]
			out[free[i]] = g.ref
			taken[free[i]] = true
		}
	}
	return out
}
