package drawproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Instant Wins v2: constrained, pre-decided prize pattern.
//
// In v2 the seed decides which SALE POSITIONS (1 = first ticket sold) carry a
// prize, subject to a set of admin-entered rules drawn from a fixed vocabulary.
// Tickets are then issued in position order at purchase, so there is no
// randomness at purchase time. The rule VALUES are per round and are digested
// into the pre-sales commitment; the rule TYPES are fixed here so an independent
// verifier can replay the placement from (totalTickets, units, rules, seed).

// AlgorithmVersionV2 is the value carried in CommitRecord.AlgorithmVersion for
// rounds allocated with AllocateInstantPrizesV2. Zero / absent means v1.
const AlgorithmVersionV2 = 2

// DefaultMaxAttempts bounds the rejection loop used by MIN_GAP / DENSITY rules
// when InstantRules.MaxAttempts is zero.
const DefaultMaxAttempts = 1000

// RuleType is one of the fixed placement-rule kinds.
type RuleType string

const (
	// RuleWindow: matching units may only land inside [MinFraction, MaxFraction]
	// of the sale. Placed directly, no discards.
	RuleWindow RuleType = "WINDOW"
	// RuleExclude: matching units may never land inside [MinFraction, MaxFraction].
	// Placed directly, no discards.
	RuleExclude RuleType = "EXCLUDE"
	// RuleMinGap: all matching units are at least Gap positions apart. Enforced by
	// bounded rejection.
	RuleMinGap RuleType = "MIN_GAP"
	// RuleDensity: at most MaxUnits matching units in any segment of
	// SegmentFraction of the sale. Enforced by bounded rejection.
	RuleDensity RuleType = "DENSITY"
	// RuleQuota: exactly Counts[k] matching units land in segment k of
	// Segments equal segments of the sale. Placed directly, no discards. This
	// is how an operator-chosen "shape" of the sale is expressed: the quota
	// fixes how many prizes fall in each bracket, the RNG alone decides where
	// inside the bracket.
	RuleQuota RuleType = "QUOTA"
)

// QuotaSegments is the bracket resolution used when a quota is derived from a
// candidate placement (QuotaFromPlacement). Ten brackets keep enough
// randomness inside each bracket that knowing the quota does not locate a
// prize.
const QuotaSegments = 10

// PositionRule is one admin-entered rule. Exactly one selector must be set:
// UnitRef (one prize unit reference) or MinAmount (every unit whose reference
// carries an amount >= MinAmount).
type PositionRule struct {
	Type            RuleType `json:"type"`
	UnitRef         string   `json:"unitRef,omitempty"`
	MinAmount       *float64 `json:"minAmount,omitempty"`
	MinFraction     float64  `json:"minFraction,omitempty"`
	MaxFraction     float64  `json:"maxFraction,omitempty"`
	Gap             int      `json:"gap,omitempty"`
	SegmentFraction float64  `json:"segmentFraction,omitempty"`
	MaxUnits        int      `json:"maxUnits,omitempty"`
	Segments        int      `json:"segments,omitempty"` // QUOTA: number of equal brackets
	Counts          []int    `json:"counts,omitempty"`   // QUOTA: units per bracket, len == Segments
}

// InstantRules is the complete per-round rule set.
type InstantRules struct {
	Version     int            `json:"version"`
	Rules       []PositionRule `json:"rules"`
	MaxAttempts int            `json:"maxAttempts,omitempty"`
}

// PreviewStats summarises a placement for humans: where each prize unit type
// landed and how the prizes spread across ten equal segments of the sale.
type PreviewStats struct {
	PerUnit    []UnitSpan `json:"perUnit"`
	PerSegment []int      `json:"perSegment"`
	Attempts   int        `json:"attempts"`
}

// UnitSpan is the first and last sale position occupied by one unit reference.
type UnitSpan struct {
	UnitRef string `json:"unitRef"`
	Units   int    `json:"units"`
	First   int    `json:"first"`
	Last    int    `json:"last"`
}

// ErrInfeasible is wrapped by every feasibility failure so callers can
// distinguish a bad rule set from an internal error.
var ErrInfeasible = errors.New("rules infeasible")

// ErrAttemptsExhausted is returned when MIN_GAP / DENSITY rules could not be
// satisfied within MaxAttempts.
var ErrAttemptsExhausted = errors.New("placement attempts exhausted")

// ---------------------------------------------------------------------------
// Validation and canonical form
// ---------------------------------------------------------------------------

// Validate checks structural validity of the rule set (not feasibility).
func (r InstantRules) Validate() error {
	if r.Version != AlgorithmVersionV2 {
		return fmt.Errorf("rules version must be %d, got %d", AlgorithmVersionV2, r.Version)
	}
	if r.MaxAttempts < 0 {
		return fmt.Errorf("maxAttempts must be >= 0")
	}
	for i, pr := range r.Rules {
		hasRef := pr.UnitRef != ""
		hasAmt := pr.MinAmount != nil
		if hasRef == hasAmt {
			return fmt.Errorf("rule %d: exactly one of unitRef or minAmount must be set", i)
		}
		if hasAmt && *pr.MinAmount < 0 {
			return fmt.Errorf("rule %d: minAmount must be >= 0", i)
		}
		switch pr.Type {
		case RuleWindow, RuleExclude:
			if pr.MinFraction < 0 || pr.MaxFraction > 1 || pr.MinFraction >= pr.MaxFraction {
				return fmt.Errorf("rule %d: need 0 <= minFraction < maxFraction <= 1", i)
			}
			if pr.Gap != 0 || pr.SegmentFraction != 0 || pr.MaxUnits != 0 || pr.Segments != 0 || len(pr.Counts) != 0 {
				return fmt.Errorf("rule %d: %s takes only minFraction/maxFraction", i, pr.Type)
			}
		case RuleMinGap:
			if pr.Gap < 1 {
				return fmt.Errorf("rule %d: gap must be >= 1", i)
			}
			if pr.MinFraction != 0 || pr.MaxFraction != 0 || pr.SegmentFraction != 0 || pr.MaxUnits != 0 || pr.Segments != 0 || len(pr.Counts) != 0 {
				return fmt.Errorf("rule %d: MIN_GAP takes only gap", i)
			}
		case RuleDensity:
			if pr.SegmentFraction <= 0 || pr.SegmentFraction > 1 {
				return fmt.Errorf("rule %d: need 0 < segmentFraction <= 1", i)
			}
			if pr.MaxUnits < 0 {
				return fmt.Errorf("rule %d: maxUnits must be >= 0", i)
			}
			if pr.MinFraction != 0 || pr.MaxFraction != 0 || pr.Gap != 0 || pr.Segments != 0 || len(pr.Counts) != 0 {
				return fmt.Errorf("rule %d: DENSITY takes only segmentFraction/maxUnits", i)
			}
		case RuleQuota:
			if pr.Segments < 2 || pr.Segments > 100 {
				return fmt.Errorf("rule %d: QUOTA needs 2 <= segments <= 100", i)
			}
			if len(pr.Counts) != pr.Segments {
				return fmt.Errorf("rule %d: QUOTA needs exactly %d counts, got %d", i, pr.Segments, len(pr.Counts))
			}
			for k, c := range pr.Counts {
				if c < 0 {
					return fmt.Errorf("rule %d: QUOTA count for segment %d must be >= 0", i, k+1)
				}
			}
			if pr.MinFraction != 0 || pr.MaxFraction != 0 || pr.Gap != 0 || pr.SegmentFraction != 0 || pr.MaxUnits != 0 {
				return fmt.Errorf("rule %d: QUOTA takes only segments/counts", i)
			}
		default:
			return fmt.Errorf("rule %d: unknown rule type %q", i, pr.Type)
		}
	}
	return nil
}

// canonicalRule mirrors PositionRule with a fixed field order for encoding.
type canonicalRule struct {
	Type            RuleType `json:"type"`
	UnitRef         string   `json:"unitRef,omitempty"`
	MinAmount       *float64 `json:"minAmount,omitempty"`
	MinFraction     *float64 `json:"minFraction,omitempty"`
	MaxFraction     *float64 `json:"maxFraction,omitempty"`
	Gap             int      `json:"gap,omitempty"`
	SegmentFraction *float64 `json:"segmentFraction,omitempty"`
	MaxUnits        *int     `json:"maxUnits,omitempty"`
	Segments        int      `json:"segments,omitempty"`
	Counts          []int    `json:"counts,omitempty"`
}

type canonicalRules struct {
	Version     int             `json:"version"`
	Rules       []canonicalRule `json:"rules"`
	MaxAttempts int             `json:"maxAttempts"`
}

func fmtFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func canonicalKey(r PositionRule) string {
	amt := ""
	if r.MinAmount != nil {
		amt = fmtFloat(*r.MinAmount)
	}
	return string(r.Type) + "\x00" + r.UnitRef + "\x00" + amt + "\x00" +
		fmtFloat(r.MinFraction) + "\x00" + fmtFloat(r.MaxFraction) + "\x00" +
		strconv.Itoa(r.Gap) + "\x00" + fmtFloat(r.SegmentFraction) + "\x00" + strconv.Itoa(r.MaxUnits) +
		"\x00" + strconv.Itoa(r.Segments) + "\x00" + fmt.Sprint(r.Counts)
}

// CanonicalRules returns the canonical JSON encoding of a rule set and its
// SHA-256 hex digest. Rules are sorted, fields appear in a fixed order, absent
// fields are omitted, floats are encoded by encoding/json (shortest round-trip
// decimal, e.g. 0.5, 1, 100), and there is no whitespace, so the platform and
// any verifier produce byte-identical output for the same logical rule set.
// MaxAttempts is normalised to its effective value so 0 and 1000 digest
// identically. The output is itself valid input to ParseRules.
func CanonicalRules(r InstantRules) (string, string, error) {
	if err := r.Validate(); err != nil {
		return "", "", err
	}
	rules := make([]PositionRule, len(r.Rules))
	copy(rules, r.Rules)
	sort.SliceStable(rules, func(i, j int) bool { return canonicalKey(rules[i]) < canonicalKey(rules[j]) })

	cr := canonicalRules{Version: r.Version, Rules: make([]canonicalRule, 0, len(rules)), MaxAttempts: r.effectiveMaxAttempts()}
	for _, pr := range rules {
		c := canonicalRule{Type: pr.Type, UnitRef: pr.UnitRef, Gap: pr.Gap}
		if pr.MinAmount != nil {
			amt := *pr.MinAmount
			c.MinAmount = &amt
		}
		switch pr.Type {
		case RuleWindow, RuleExclude:
			lo, hi := pr.MinFraction, pr.MaxFraction
			c.MinFraction, c.MaxFraction = &lo, &hi
		case RuleDensity:
			sf := pr.SegmentFraction
			mu := pr.MaxUnits
			c.SegmentFraction, c.MaxUnits = &sf, &mu
		case RuleQuota:
			c.Segments = pr.Segments
			c.Counts = append([]int(nil), pr.Counts...)
		}
		cr.Rules = append(cr.Rules, c)
	}
	b, err := json.Marshal(cr)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}

// ParseRules decodes a rule set from JSON (canonical or not) and validates it.
func ParseRules(s string) (InstantRules, error) {
	var r InstantRules
	if strings.TrimSpace(s) == "" {
		return InstantRules{Version: AlgorithmVersionV2}, nil
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, fmt.Errorf("invalid rules JSON: %v", err)
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	return r, nil
}

func (r InstantRules) effectiveMaxAttempts() int {
	if r.MaxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return r.MaxAttempts
}

// ---------------------------------------------------------------------------
// Matching and windows
// ---------------------------------------------------------------------------

// unitAmount extracts the "amount=" component of a prize-unit reference of the
// form "pool=<id>;type=<type>;amount=<amount>". ok is false when the reference
// carries no numeric amount (e.g. "amount=nil"), in which case MinAmount
// selectors never match it.
func unitAmount(ref string) (float64, bool) {
	for _, part := range strings.Split(ref, ";") {
		if strings.HasPrefix(part, "amount=") {
			v, err := strconv.ParseFloat(strings.TrimPrefix(part, "amount="), 64)
			if err != nil {
				return 0, false
			}
			return v, true
		}
	}
	return 0, false
}

// Matches reports whether the rule's selector applies to a unit reference.
func (pr PositionRule) Matches(ref string) bool {
	if pr.UnitRef != "" {
		return pr.UnitRef == ref
	}
	if pr.MinAmount != nil {
		a, ok := unitAmount(ref)
		return ok && a >= *pr.MinAmount
	}
	return false
}

// WindowPositions converts a fractional window to inclusive 1-based positions:
// lo = floor(min*M)+1, hi = floor(max*M).
func WindowPositions(totalTickets int, minFraction, maxFraction float64) (lo, hi int) {
	lo = int(minFraction*float64(totalTickets)) + 1
	hi = int(maxFraction * float64(totalTickets))
	if lo < 1 {
		lo = 1
	}
	if hi > totalTickets {
		hi = totalTickets
	}
	return lo, hi
}

// SegmentLength converts a segment fraction to a whole number of positions (>=1).
func SegmentLength(totalTickets int, fraction float64) int {
	l := int(fraction * float64(totalTickets))
	if l < 1 {
		l = 1
	}
	return l
}

// QuotaSegmentBounds returns the inclusive 1-based position range of bracket k
// (0-based) when the sale is split into `segments` equal brackets. Brackets
// partition [1, totalTickets] exactly: bracket k is
// (floor(k*M/S), floor((k+1)*M/S)].
func QuotaSegmentBounds(totalTickets, segments, k int) (lo, hi int) {
	lo = k*totalTickets/segments + 1
	hi = (k + 1) * totalTickets / segments
	return lo, hi
}

// QuotaSegmentOf returns the 0-based bracket a position falls in.
func QuotaSegmentOf(totalTickets, segments, position int) int {
	for k := 0; k < segments; k++ {
		if lo, hi := QuotaSegmentBounds(totalTickets, segments, k); position >= lo && position <= hi {
			return k
		}
	}
	return segments - 1
}

// QuotaFromPlacement derives QUOTA rules from a candidate placement: one rule
// per unit reference with the number of units that landed in each of
// `segments` brackets. This is the operator-facing "use this shape" step: the
// candidate itself is discarded, only its bracket distribution is kept, and a
// fresh seed places prizes inside the brackets at mint.
func QuotaFromPlacement(totalTickets int, placement map[int]string, segments int) []PositionRule {
	perRef := map[string][]int{}
	for p, ref := range placement {
		if _, ok := perRef[ref]; !ok {
			perRef[ref] = make([]int, segments)
		}
		perRef[ref][QuotaSegmentOf(totalTickets, segments, p)]++
	}
	refs := make([]string, 0, len(perRef))
	for ref := range perRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	rules := make([]PositionRule, 0, len(refs))
	for _, ref := range refs {
		rules = append(rules, PositionRule{Type: RuleQuota, UnitRef: ref, Segments: segments, Counts: perRef[ref]})
	}
	return rules
}

// allowedSet computes the positions (1-based, index 0 unused) a unit may occupy
// after applying every WINDOW and EXCLUDE rule that matches it.
func allowedSet(totalTickets int, ref string, rules InstantRules) []bool {
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

type unitGroup struct {
	ref     string
	count   int
	allowed []bool
	size    int // number of allowed positions
	segment int // QUOTA sub-group bracket (0-based), -1 otherwise
}

// quotaFor returns the single QUOTA rule matching a unit reference, or nil.
// More than one is rejected by CheckFeasibility.
func quotaFor(ref string, rules InstantRules) (*PositionRule, int) {
	var found *PositionRule
	n := 0
	for i := range rules.Rules {
		pr := &rules.Rules[i]
		if pr.Type == RuleQuota && pr.Matches(ref) {
			found = pr
			n++
		}
	}
	return found, n
}

// buildGroups groups units by reference, computes allowed sets, and orders the
// groups most-constrained first (smallest allowed set, then by reference).
func buildGroups(totalTickets int, units []string, rules InstantRules) []*unitGroup {
	byRef := map[string]*unitGroup{}
	var order []string
	for _, u := range units {
		g, ok := byRef[u]
		if !ok {
			g = &unitGroup{ref: u}
			byRef[u] = g
			order = append(order, u)
		}
		g.count++
	}
	groups := make([]*unitGroup, 0, len(order))
	for _, ref := range order {
		g := byRef[ref]
		g.segment = -1
		base := allowedSet(totalTickets, ref, rules)
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
			sg := &unitGroup{ref: ref, count: q.Counts[k], segment: k, allowed: make([]bool, totalTickets+1)}
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

// CheckFeasibility verifies, without consuming any randomness, that every unit
// can be placed whatever the seed turns out to be. For direct-placement rules it
// uses a worst-case bound: every earlier (more constrained) group may consume
// positions inside a later group's allowed set. For rejection rules it checks
// the trivial counting bounds only; a rule set that passes here can still
// exhaust MaxAttempts if it is very tight.
func CheckFeasibility(totalTickets int, units []string, rules InstantRules) error {
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
	groups := buildGroups(totalTickets, units, rules)
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

func (r InstantRules) hasRejectionRules() bool {
	for _, pr := range r.Rules {
		if pr.Type == RuleMinGap || pr.Type == RuleDensity {
			return true
		}
	}
	return false
}

// placeOnce performs one direct placement pass using the given DRBG.
func placeOnce(totalTickets int, groups []*unitGroup, d *drbg) map[int]string {
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

// CheckRules reports whether a placement satisfies every rule (all four types),
// returning the first violation as a descriptive error.
func CheckRules(totalTickets int, placement map[int]string, rules InstantRules) error {
	for _, pr := range rules.Rules {
		// Collect matching positions in ascending order.
		var pos []int
		for p, ref := range placement {
			if pr.Matches(ref) {
				pos = append(pos, p)
			}
		}
		sort.Ints(pos)
		switch pr.Type {
		case RuleWindow:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			for _, p := range pos {
				if p < lo || p > hi {
					return fmt.Errorf("WINDOW violated: %s at position %d outside [%d,%d]", placement[p], p, lo, hi)
				}
			}
		case RuleExclude:
			lo, hi := WindowPositions(totalTickets, pr.MinFraction, pr.MaxFraction)
			for _, p := range pos {
				if p >= lo && p <= hi {
					return fmt.Errorf("EXCLUDE violated: %s at position %d inside [%d,%d]", placement[p], p, lo, hi)
				}
			}
		case RuleMinGap:
			for i := 1; i < len(pos); i++ {
				if pos[i]-pos[i-1] < pr.Gap {
					return fmt.Errorf("MIN_GAP violated: positions %d and %d closer than %d", pos[i-1], pos[i], pr.Gap)
				}
			}
		case RuleQuota:
			got := make([]int, pr.Segments)
			for _, p := range pos {
				got[QuotaSegmentOf(totalTickets, pr.Segments, p)]++
			}
			for k := range got {
				if got[k] != pr.Counts[k] {
					return fmt.Errorf("QUOTA violated: bracket %d holds %d units, quota is %d", k+1, got[k], pr.Counts[k])
				}
			}
		case RuleDensity:
			segLen := SegmentLength(totalTickets, pr.SegmentFraction)
			counts := map[int]int{}
			for _, p := range pos {
				seg := (p - 1) / segLen
				counts[seg]++
				if counts[seg] > pr.MaxUnits {
					return fmt.Errorf("DENSITY violated: segment %d holds more than %d units", seg+1, pr.MaxUnits)
				}
			}
		}
	}
	return nil
}

// AllocateInstantPrizesV2 deterministically assigns each prize unit to a distinct
// SALE POSITION in [1, totalTickets] as a pure function of (totalTickets, units,
// rules, seed). It returns the placement and the number of attempts consumed
// (always 1 unless MIN_GAP / DENSITY rules forced rejection).
//
// Groups of identical units are placed most-constrained first by partial
// Fisher–Yates over the positions still free inside their allowed set, so each
// group is uniform within its window and without replacement. When rejection
// rules exist the whole placement is re-drawn from the SAME DRBG stream until
// they hold or MaxAttempts is reached; a verifier replays exactly that many
// attempts.
func AllocateInstantPrizesV2(totalTickets int, units []string, rules InstantRules, seed []byte) (map[int]string, int, error) {
	if err := CheckFeasibility(totalTickets, units, rules); err != nil {
		return nil, 0, err
	}
	if len(units) == 0 {
		return map[int]string{}, 1, nil
	}
	groups := buildGroups(totalTickets, units, rules)
	d := newDRBG(seed)
	maxAttempts := rules.effectiveMaxAttempts()
	reject := rules.hasRejectionRules()
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		placement := placeOnce(totalTickets, groups, d)
		if !reject {
			return placement, attempt, nil
		}
		if err := CheckRules(totalTickets, placement, rules); err == nil {
			return placement, attempt, nil
		}
	}
	return nil, maxAttempts, fmt.Errorf("%w after %d attempts", ErrAttemptsExhausted, maxAttempts)
}

// PreviewInstantPrizes runs AllocateInstantPrizesV2 with a fresh, discarded
// crypto/rand seed so an operator can see what a rule set produces without any
// outcome being decided. The seed is never returned.
func PreviewInstantPrizes(totalTickets int, units []string, rules InstantRules) (map[int]string, PreviewStats, error) {
	seed, err := GenerateSeed()
	if err != nil {
		return nil, PreviewStats{}, err
	}
	placement, attempts, err := AllocateInstantPrizesV2(totalTickets, units, rules, seed)
	for i := range seed {
		seed[i] = 0
	}
	if err != nil {
		return nil, PreviewStats{}, err
	}
	return placement, SummarisePlacement(totalTickets, placement, attempts), nil
}

// SummarisePlacement computes PreviewStats for a placement.
func SummarisePlacement(totalTickets int, placement map[int]string, attempts int) PreviewStats {
	spans := map[string]*UnitSpan{}
	segs := make([]int, 10)
	for p, ref := range placement {
		s, ok := spans[ref]
		if !ok {
			s = &UnitSpan{UnitRef: ref, First: p, Last: p}
			spans[ref] = s
		}
		s.Units++
		if p < s.First {
			s.First = p
		}
		if p > s.Last {
			s.Last = p
		}
		if totalTickets > 0 {
			seg := (p - 1) * 10 / totalTickets
			if seg > 9 {
				seg = 9
			}
			segs[seg]++
		}
	}
	out := PreviewStats{PerSegment: segs, Attempts: attempts}
	for _, s := range spans {
		out.PerUnit = append(out.PerUnit, *s)
	}
	sort.Slice(out.PerUnit, func(i, j int) bool { return out.PerUnit[i].UnitRef < out.PerUnit[j].UnitRef })
	return out
}

// ---------------------------------------------------------------------------
// Display permutation
// ---------------------------------------------------------------------------

const displayDomain = "drawproof/display/v2"

// DisplayPermutation returns the ticket number shown for each sale position:
// result[p-1] is the display number of position p. It is a full Fisher–Yates
// over [1, totalTickets] driven by a domain-separated sub-seed, so publishing
// it reveals nothing about the placement stream.
func DisplayPermutation(totalTickets int, seed []byte) []int {
	if totalTickets <= 0 {
		return nil
	}
	h := sha256.New()
	h.Write([]byte(displayDomain))
	h.Write(seed)
	d := newDRBG(h.Sum(nil))
	perm := make([]int, totalTickets)
	for i := range perm {
		perm[i] = i + 1
	}
	for i := 0; i < totalTickets-1; i++ {
		j := i + d.intn(totalTickets-i)
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm
}

// DigestPermutation returns the canonical ordered digest of a permutation.
func DigestPermutation(perm []int) string {
	items := make([]string, len(perm))
	for i, n := range perm {
		items[i] = strconv.Itoa(n)
	}
	return DigestStringsOrdered(items)
}
