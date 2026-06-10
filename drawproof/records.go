package drawproof

import "fmt"

// DrawKind identifies which mechanic a ledger record relates to.
type DrawKind string

const (
	KindMainDraw DrawKind = "MAIN_DRAW"
	KindInstant  DrawKind = "INSTANT"
	KindVault    DrawKind = "VAULT"
)

// CommitRecord is written to the immutable ledger BEFORE a draw is run (for the
// main/vault draws) or BEFORE instant-win sales open. It binds the platform to a
// fixed input set and a fixed local seed, and names the beacon pulse whose value
// will be folded in — without yet revealing the outcome, so it cannot later be
// altered or back-dated.
//
// SeedHash is the hash of the platform's LOCAL seed component (known at commit
// time). The final seed is local seed mixed with the named beacon pulse value;
// for a FUTURE pulse that value does not exist at commit time, which is what
// prevents the platform from grinding a favourable outcome.
type CommitRecord struct {
	Kind        DrawKind `json:"kind"`
	RoundID     int      `json:"roundId"`
	DrawID      int      `json:"drawId,omitempty"`
	InputDigest string   `json:"inputDigest"` // frozen pool digest, or instant allocation digest
	SeedHash    string   `json:"seedHash"`    // SHA-256 of the local seed component
	BeaconChain string   `json:"beaconChain,omitempty"`
	BeaconPulse uint64   `json:"beaconPulse,omitempty"` // nominated beacon pulse number
	AdminID     int      `json:"adminId,omitempty"`
	CommittedAt string   `json:"committedAt"` // RFC3339
}

// RevealRecord is written to the immutable ledger AFTER a draw, disclosing the
// seed and the winning outcome so any third party can reproduce the result.
type RevealRecord struct {
	Kind            DrawKind `json:"kind"`
	RoundID         int      `json:"roundId"`
	DrawID          int      `json:"drawId,omitempty"`
	Seed            string   `json:"seed"`                      // hex of the final seed fed to the algorithm
	LocalSeed       string   `json:"localSeed,omitempty"`       // hex of the platform-generated component
	BeaconPulse     uint64   `json:"beaconPulse,omitempty"`     // beacon pulse number used
	BeaconValue     string   `json:"beaconValue,omitempty"`     // hex randomness of the beacon pulse
	BeaconPulseTime string   `json:"beaconPulseTime,omitempty"` // RFC3339 scheduled time of the pulse
	Winners         []string `json:"winners,omitempty"`         // awarded entry identifiers, in selection order
	WinnerDigest    string   `json:"winnerDigest"`              // ordered digest of the winners/allocation
	TotalTickets    int      `json:"totalTickets,omitempty"`    // instant: ticket population size, needed to reproduce the allocation
	AdminID         int      `json:"adminId,omitempty"`         // admin who executed the draw (0 = system/automated)
	ResultedAt      string   `json:"resultedAt"`                // RFC3339
}

// CommitKey is the immudb key under which a commit is stored.
func CommitKey(kind DrawKind, roundID int) string {
	return fmt.Sprintf("draw:commit:%s:%d", kind, roundID)
}

// RevealKey is the immudb key under which a reveal is stored.
func RevealKey(kind DrawKind, roundID int) string {
	return fmt.Sprintf("draw:reveal:%s:%d", kind, roundID)
}
