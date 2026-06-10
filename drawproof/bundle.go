package drawproof

import (
	"fmt"
	"strconv"
)

// Bundle is the self-contained proof artefact for one draw: the published input
// set plus the commit and reveal records read from the immutable ledger. It is
// exactly what the in-browser verifier consumes and what the public verification
// endpoints emit, so the shape is defined once here and shared by both.
type Bundle struct {
	ID         string            `json:"id,omitempty"`
	Kind       DrawKind          `json:"kind"`
	Pool       []string          `json:"pool,omitempty"`       // MAIN_DRAW / VAULT: frozen entry identifiers
	Allocation map[string]string `json:"allocation,omitempty"` // INSTANT: ticketNumber -> prize reference
	Commit     CommitRecord      `json:"commit"`
	Reveal     RevealRecord      `json:"reveal"`
}

// VerifyBundle dispatches a bundle to the correct verification path and returns
// the per-check result. Instant bundles with a disclosed seed are verified by
// full reproduction-from-seed; without a seed they fall back to a digest match.
func VerifyBundle(b Bundle) (VerifyResult, error) {
	switch b.Kind {
	case KindMainDraw, KindVault:
		return VerifyMainDraw(b.Pool, b.Commit, b.Reveal), nil
	case KindInstant:
		alloc := parseIntKeyMap(b.Allocation)
		if b.Reveal.Seed != "" {
			return VerifyInstantFromSeed(b.Reveal.TotalTickets, alloc, b.Commit, b.Reveal), nil
		}
		return VerifyInstant(alloc, b.Commit), nil
	default:
		return VerifyResult{}, fmt.Errorf("unknown draw kind %q", b.Kind)
	}
}

// parseIntKeyMap converts a JSON string-keyed allocation map back to integer
// ticket numbers, ignoring any non-integer keys.
func parseIntKeyMap(in map[string]string) map[int]string {
	out := make(map[int]string, len(in))
	for k, v := range in {
		if n, err := strconv.Atoi(k); err == nil {
			out[n] = v
		}
	}
	return out
}
