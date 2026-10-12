package drawproof

import (
	"strings"
	"testing"
)

func TestVerifyKeyAndDigestChunks(t *testing.T) {
	if got := VerifyKey(KindInstant, 812); got != "draw:verify:INSTANT:812" {
		t.Fatalf("VerifyKey = %q", got)
	}
	a := DigestChunks([]string{"aa", "bb"})
	b := DigestChunks([]string{"aabb"})
	if a == b {
		t.Fatal("chunk digests must be framed so a re-split list hashes differently")
	}
	if DigestChunks([]string{"aa", "bb"}) != a {
		t.Fatal("DigestChunks is not deterministic")
	}
}

func TestVerifyV2BundleWithMintRecord(t *testing.T) {
	const M = 800
	b := buildV2Bundle(t, M, testUnits(), topHalfRules(), 5)

	// Without a record the bundle verifies and the check is absent.
	res, err := VerifyBundle(b)
	if err != nil || !res.OK {
		t.Fatalf("bundle without record: err=%v failing=%v", err, failing(res))
	}
	for _, c := range res.Checks {
		if c.Name == "mint-verified-before-sales" {
			t.Fatal("check must not appear without a record")
		}
	}

	good := &VerifyRecord{
		Kind: KindInstant, RoundID: 1, Outcome: VerifyOutcomeVerified, AlgorithmVersion: 2,
		TotalTickets: M, PositionsWalked: M, InputDigest: b.Commit.InputDigest,
		RulesDigest: b.Commit.RulesDigest, DisplayDigest: b.Commit.DisplayDigest,
		ReproducedFromSeed: true, Verifier: "test", StartedAt: "2026-10-12T09:00:00Z", FinishedAt: "2026-10-12T09:05:00Z",
	}
	b.Verify = good
	res, _ = VerifyBundle(b)
	if !res.OK {
		t.Fatalf("bundle with a matching record failed: %v", failing(res))
	}
	found := false
	for _, c := range res.Checks {
		if c.Name == "mint-verified-before-sales" && c.OK {
			found = true
		}
	}
	if !found {
		t.Fatal("matching record should add a passing check")
	}

	// The sealed path reports it too.
	sealed := b
	sealed.Reveal = RevealRecord{}
	res, _ = VerifyBundle(sealed)
	found = false
	for _, c := range res.Checks {
		if c.Name == "mint-verified-before-sales" && c.OK {
			found = true
		}
	}
	if !found {
		t.Fatalf("sealed bundle should carry the record check: %v", res.Checks)
	}

	cases := map[string]func(v *VerifyRecord){
		"failed outcome":     func(v *VerifyRecord) { v.Outcome = VerifyOutcomeFailed; v.Error = "position 7 is missing" },
		"other round":        func(v *VerifyRecord) { v.RoundID = 2 },
		"allocation differs": func(v *VerifyRecord) { v.InputDigest = "00" },
		"display differs":    func(v *VerifyRecord) { v.DisplayDigest = "00" },
		"short walk":         func(v *VerifyRecord) { v.PositionsWalked = M - 1 },
		"rules differ":       func(v *VerifyRecord) { v.RulesDigest = "00" },
	}
	for name, mutate := range cases {
		v := *good
		mutate(&v)
		bad := b
		bad.Verify = &v
		res, _ = VerifyBundle(bad)
		if res.OK {
			t.Errorf("%s: bundle should fail", name)
			continue
		}
		f := failing(res)
		if len(f) != 1 || !strings.HasPrefix(f[0], "mint-verified-before-sales") {
			t.Errorf("%s: expected only the record check to fail, got %v", name, f)
		}
	}
}
