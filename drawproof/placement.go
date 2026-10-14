package drawproof

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
)

// Placement is an instant allocation held as one small integer per sale
// position: refs lists the distinct unit references (index 0 is "no prize")
// and byPos[p] is the index of the prize at position p. Ten million positions
// are 20 MB. The same allocation as a map[int]string, one entry per prize, was
// several hundred megabytes at seven million prizes, and the mint, the
// verifier and the preview each held one; the map form is still available
// through Map and the map-taking functions, which wrap this type.
type Placement struct {
	refs  []string
	byPos []uint16 // len totalTickets+1; byPos[0] is unused
	count int
}

// maxPlacementRefs is how many distinct unit references a Placement can hold.
const maxPlacementRefs = 1<<16 - 1

// NewPlacement returns an empty placement over positions 1..totalTickets.
func NewPlacement(totalTickets int) *Placement {
	if totalTickets < 0 {
		totalTickets = 0
	}
	return &Placement{refs: []string{""}, byPos: make([]uint16, totalTickets+1)}
}

// PlacementFromMap converts a position -> reference map. Positions outside
// 1..totalTickets are an error.
func PlacementFromMap(totalTickets int, m map[int]string) (*Placement, error) {
	p := NewPlacement(totalTickets)
	for pos, ref := range m {
		if err := p.Set(pos, ref); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// TotalTickets is the number of positions the placement spans.
func (p *Placement) TotalTickets() int { return len(p.byPos) - 1 }

// Len is the number of placed prizes.
func (p *Placement) Len() int { return p.count }

// refIndex returns the index of ref in refs, adding it when new. References
// are few (one per prize pool), so a scan beats a map.
func (p *Placement) refIndex(ref string) (uint16, error) {
	for i, r := range p.refs {
		if i > 0 && r == ref {
			return uint16(i), nil
		}
	}
	if len(p.refs) > maxPlacementRefs {
		return 0, fmt.Errorf("placement holds more than %d distinct unit references", maxPlacementRefs)
	}
	p.refs = append(p.refs, ref)
	return uint16(len(p.refs) - 1), nil
}

// Set places ref at pos, replacing any prize already there. An empty ref
// clears the position.
func (p *Placement) Set(pos int, ref string) error {
	if pos < 1 || pos >= len(p.byPos) {
		return fmt.Errorf("position %d outside 1..%d", pos, p.TotalTickets())
	}
	if ref == "" {
		p.set(pos, 0)
		return nil
	}
	idx, err := p.refIndex(ref)
	if err != nil {
		return err
	}
	p.set(pos, idx)
	return nil
}

func (p *Placement) set(pos int, idx uint16) {
	was := p.byPos[pos]
	p.byPos[pos] = idx
	if was == 0 && idx != 0 {
		p.count++
	} else if was != 0 && idx == 0 {
		p.count--
	}
}

// Ref returns the reference placed at pos, and false when the position holds
// no prize or is out of range.
func (p *Placement) Ref(pos int) (string, bool) {
	if pos < 1 || pos >= len(p.byPos) || p.byPos[pos] == 0 {
		return "", false
	}
	return p.refs[p.byPos[pos]], true
}

// IndexAt returns the reference index at pos (0 when no prize), as Refs
// indexes it. Callers that key their own tables by reference use it to avoid
// a string lookup per position.
func (p *Placement) IndexAt(pos int) int {
	if pos < 1 || pos >= len(p.byPos) {
		return 0
	}
	return int(p.byPos[pos])
}

// Refs returns the distinct references, aligned with IndexAt: Refs()[0] is ""
// for "no prize".
func (p *Placement) Refs() []string {
	out := make([]string, len(p.refs))
	copy(out, p.refs)
	return out
}

// Range calls fn for every placed prize in ascending position order, stopping
// when fn returns false.
func (p *Placement) Range(fn func(pos int, ref string) bool) {
	for pos := 1; pos < len(p.byPos); pos++ {
		if idx := p.byPos[pos]; idx != 0 {
			if !fn(pos, p.refs[idx]) {
				return
			}
		}
	}
}

// Map returns the placement as position -> reference.
func (p *Placement) Map() map[int]string {
	out := make(map[int]string, p.count)
	p.Range(func(pos int, ref string) bool {
		out[pos] = ref
		return true
	})
	return out
}

// Counts returns the placed units as a multiset.
func (p *Placement) Counts() UnitCounts {
	out := UnitCounts{}
	for pos := 1; pos < len(p.byPos); pos++ {
		if idx := p.byPos[pos]; idx != 0 {
			out[p.refs[idx]]++
		}
	}
	return out
}

// Equal reports whether q holds the same prize at every position.
func (p *Placement) Equal(q *Placement) bool {
	if p == nil || q == nil {
		return p == q
	}
	if len(p.byPos) != len(q.byPos) || p.count != q.count {
		return false
	}
	for pos := 1; pos < len(p.byPos); pos++ {
		pi, qi := p.byPos[pos], q.byPos[pos]
		if (pi == 0) != (qi == 0) {
			return false
		}
		if pi != 0 && p.refs[pi] != q.refs[qi] {
			return false
		}
	}
	return true
}

// Digest is DigestAllocation of the same placement: SHA-256 over the prizes
// in ascending position, each as its framed position then framed reference.
// It walks the positions in order, so there is no key sort.
func (p *Placement) Digest() string {
	h := sha256.New()
	var buf [32]byte
	for pos := 1; pos < len(p.byPos); pos++ {
		idx := p.byPos[pos]
		if idx == 0 {
			continue
		}
		s := strconv.AppendInt(buf[8:8], int64(pos), 10)
		binary.BigEndian.PutUint64(buf[:8], uint64(len(s)))
		_, _ = h.Write(buf[:8+len(s)])
		writeFramed(h, p.refs[idx])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Quota is QuotaFromPlacement for this placement: one QUOTA rule per
// reference with the number of its units in each of `segments` brackets.
func (p *Placement) Quota(segments int) []PositionRule {
	if segments <= 0 {
		return nil
	}
	total := p.TotalTickets()
	perRef := make([][]int, len(p.refs))
	for k := 0; k < segments; k++ {
		lo, hi := QuotaSegmentBounds(total, segments, k)
		for pos := lo; pos <= hi && pos < len(p.byPos); pos++ {
			if pos < 1 {
				continue
			}
			idx := p.byPos[pos]
			if idx == 0 {
				continue
			}
			if perRef[idx] == nil {
				perRef[idx] = make([]int, segments)
			}
			perRef[idx][k]++
		}
	}
	refs := make([]string, 0, len(p.refs))
	byRef := make(map[string][]int, len(p.refs))
	for idx, counts := range perRef {
		if counts != nil {
			refs = append(refs, p.refs[idx])
			byRef[p.refs[idx]] = counts
		}
	}
	sort.Strings(refs)
	rules := make([]PositionRule, 0, len(refs))
	for _, ref := range refs {
		rules = append(rules, PositionRule{Type: RuleQuota, UnitRef: ref, Segments: segments, Counts: byRef[ref]})
	}
	return rules
}

// Summarise is SummarisePlacement for this placement.
func (p *Placement) Summarise(attempts int) PreviewStats {
	total := p.TotalTickets()
	spans := make([]*UnitSpan, len(p.refs))
	segs := make([]int, 10)
	for pos := 1; pos < len(p.byPos); pos++ {
		idx := p.byPos[pos]
		if idx == 0 {
			continue
		}
		s := spans[idx]
		if s == nil {
			s = &UnitSpan{UnitRef: p.refs[idx], First: pos, Last: pos}
			spans[idx] = s
		}
		s.Units++
		if pos < s.First {
			s.First = pos
		}
		if pos > s.Last {
			s.Last = pos
		}
		if total > 0 {
			seg := (pos - 1) * 10 / total
			if seg > 9 {
				seg = 9
			}
			segs[seg]++
		}
	}
	out := PreviewStats{PerSegment: segs, Attempts: attempts}
	for _, s := range spans {
		if s != nil {
			out.PerUnit = append(out.PerUnit, *s)
		}
	}
	sort.Slice(out.PerUnit, func(i, j int) bool { return out.PerUnit[i].UnitRef < out.PerUnit[j].UnitRef })
	return out
}

// CheckRules is CheckRules for this placement.
func (p *Placement) CheckRules(rules InstantRules) error {
	total := p.TotalTickets()
	matching := func(pr *PositionRule) []int {
		var pos []int
		for i := 1; i < len(p.refs); i++ {
			if !pr.Matches(p.refs[i]) {
				continue
			}
			idx := uint16(i)
			for q := 1; q < len(p.byPos); q++ {
				if p.byPos[q] == idx {
					pos = append(pos, q)
				}
			}
		}
		sort.Ints(pos)
		return pos
	}
	refAt := func(pos int) string { r, _ := p.Ref(pos); return r }
	return checkRulesOn(total, rules, matching, refAt)
}

// UnitCounts is a prize-unit multiset: reference -> number of units. It is the
// input the allocator works from; a unit list is counted into it, so seven
// million units need not be seven million strings.
type UnitCounts map[string]int

// CountUnits counts a unit list.
func CountUnits(units []string) UnitCounts {
	out := make(UnitCounts)
	for _, u := range units {
		out[u]++
	}
	return out
}

// Total is the number of units.
func (u UnitCounts) Total() int {
	n := 0
	for _, c := range u {
		n += c
	}
	return n
}

// sortedRefs is the references in canonical (sorted) order.
func (u UnitCounts) sortedRefs() []string {
	refs := make([]string, 0, len(u))
	for ref, c := range u {
		if c > 0 {
			refs = append(refs, ref)
		}
	}
	sort.Strings(refs)
	return refs
}
