package drawproof

// Sale positions as sorted, disjoint, inclusive intervals.
//
// A unit group's allowed set used to be a []bool of totalTickets+1 entries,
// built and scanned per group and per group pair: O(G² × M) time and ~10 MB
// per group at ten million tickets. A window, an exclusion or a quota bracket
// is a range, so the same set is a handful of intervals, and intersections and
// sizes come from a two-pointer merge. Placement then only needs "the k-th
// free position inside these intervals", which rankSet answers in O(log M).

type span struct{ lo, hi int } // inclusive, lo <= hi

type spans []span

// fullSpan is every position of a sale.
func fullSpan(totalTickets int) spans {
	if totalTickets <= 0 {
		return nil
	}
	return spans{{1, totalTickets}}
}

func (s spans) size() int {
	n := 0
	for _, x := range s {
		n += x.hi - x.lo + 1
	}
	return n
}

// intersect keeps the positions of s that lie in [lo, hi].
func (s spans) intersect(lo, hi int) spans {
	var out spans
	for _, x := range s {
		a, b := x.lo, x.hi
		if a < lo {
			a = lo
		}
		if b > hi {
			b = hi
		}
		if a <= b {
			out = append(out, span{a, b})
		}
	}
	return out
}

// subtract removes the positions of [lo, hi] from s.
func (s spans) subtract(lo, hi int) spans {
	if lo > hi {
		return s
	}
	var out spans
	for _, x := range s {
		if x.hi < lo || x.lo > hi {
			out = append(out, x)
			continue
		}
		if x.lo < lo {
			out = append(out, span{x.lo, lo - 1})
		}
		if x.hi > hi {
			out = append(out, span{hi + 1, x.hi})
		}
	}
	return out
}

// overlap is the number of positions in both s and t.
func (s spans) overlap(t spans) int {
	n, i, j := 0, 0, 0
	for i < len(s) && j < len(t) {
		lo, hi := s[i].lo, s[i].hi
		if t[j].lo > lo {
			lo = t[j].lo
		}
		if t[j].hi < hi {
			hi = t[j].hi
		}
		if lo <= hi {
			n += hi - lo + 1
		}
		if s[i].hi < t[j].hi {
			i++
		} else {
			j++
		}
	}
	return n
}

// allowedSpans computes the positions a unit may occupy after every WINDOW and
// EXCLUDE rule that matches it, as intervals. It is allowedSet expressed as
// spans: windows intersect, exclusions subtract.
func allowedSpans(totalTickets int, ref string, rules InstantRules) spans {
	s := fullSpan(totalTickets)
	for _, pr := range rules.Rules {
		if !pr.Matches(ref) {
			continue
		}
		switch pr.Type {
		case RuleWindow:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			s = s.intersect(lo, hi)
		case RuleExclude:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			s = s.subtract(lo, hi)
		}
	}
	return s
}
