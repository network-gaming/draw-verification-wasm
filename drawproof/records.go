package drawproof

import "fmt"

// DrawKind identifies which mechanic a ledger record relates to.
type DrawKind string

const (
	KindMainDraw DrawKind = "MAIN_DRAW"
	KindInstant  DrawKind = "INSTANT"
	KindVault    DrawKind = "VAULT"
)

// DrawMethod says how a main draw's winners were produced. The zero value and
// DrawMethodBeacon are the computed draw: winners are a deterministic function
// of the frozen pool and a seed mixed from the platform's sealed component and
// a public randomness beacon pulse. DrawMethodManual is a recorded draw: the
// winners were drawn outside the platform (slips from a box at an event, say),
// typed in by an operator and approved by a second one. A recorded draw is
// not reproducible from a seed; the verifier checks only that the pool was
// frozen before the draw and that every recorded winner was in it.
type DrawMethod string

const (
	DrawMethodBeacon DrawMethod = "BEACON"
	DrawMethodManual DrawMethod = "MANUAL"
)

// IsManual reports whether the method is the recorded (hand-drawn) draw.
func (m DrawMethod) IsManual() bool { return m == DrawMethodManual }

// DrawEvidence is what a recorded draw publishes in place of a seed: where the
// draw can be watched and who witnessed it, as the operator entered them.
type DrawEvidence struct {
	RecordingURL string `json:"recordingUrl,omitempty"`
	Witnesses    string `json:"witnesses,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

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

	// Method is how the main draw's winners will be produced; absent or
	// BEACON means computed from the seed, MANUAL means recorded (see
	// DrawMethod). A MANUAL commit names no beacon pulse and its SeedHash
	// is empty: it freezes the pool before the draw and nothing else.
	Method DrawMethod `json:"method,omitempty"`

	// Instant Wins v2 (AlgorithmVersion == AlgorithmVersionV2). Zero / absent
	// values mean a v1 record.
	AlgorithmVersion int    `json:"algorithmVersion,omitempty"`
	RulesDigest      string `json:"rulesDigest,omitempty"`   // SHA-256 of CanonicalRules
	DisplayDigest    string `json:"displayDigest,omitempty"` // DigestPermutation of the display permutation
	TotalTickets     int    `json:"totalTickets,omitempty"`  // ticket population, committed before sales

	// Candidate draw (instant v2 rounds minted from a stored candidate). The
	// platform generated CandidatesGenerated candidate draws for the round
	// before the mint; the one numbered CandidateSeq, created at
	// CandidateCreatedAt, is the draw that was minted, and its seed is the one
	// this record commits to. Zero / absent values mean the seed was drawn at
	// mint time.
	CandidateSeq        int    `json:"candidateSeq,omitempty"`
	CandidatesGenerated int    `json:"candidatesGenerated,omitempty"`
	CandidateCreatedAt  string `json:"candidateCreatedAt,omitempty"` // RFC3339
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
	Attempts        int      `json:"attempts,omitempty"`        // instant v2: placement attempts consumed (1 unless rejection rules)

	// Recorded (MANUAL) main draws only. Seed, LocalSeed and the beacon
	// fields are empty: the winners were drawn outside the platform. AdminID
	// is the approver; EnteredBy lists the operators who typed the winners
	// in; ExportDigest is the SHA-256 of the entries sheet the operator
	// printed from the frozen pool; Evidence is where the draw can be seen.
	Method       DrawMethod    `json:"method,omitempty"`
	EnteredBy    []int         `json:"enteredBy,omitempty"`
	ExportDigest string        `json:"exportDigest,omitempty"`
	Evidence     *DrawEvidence `json:"evidence,omitempty"`
}

// CommitKey is the immudb key under which a commit is stored.
func CommitKey(kind DrawKind, roundID int) string {
	return fmt.Sprintf("draw:commit:%s:%d", kind, roundID)
}

// RevealKey is the immudb key under which a reveal is stored.
func RevealKey(kind DrawKind, roundID int) string {
	return fmt.Sprintf("draw:reveal:%s:%d", kind, roundID)
}

// VerifyOutcome is the result a VerifyRecord reports.
type VerifyOutcome string

const (
	VerifyOutcomeVerified VerifyOutcome = "VERIFIED"
	VerifyOutcomeFailed   VerifyOutcome = "FAILED"
)

// VerifyRecord is written to the immutable ledger when the rows a platform
// minted for an Instant Wins v2 round have been read back and compared with
// the commitment: every sale position present once, every envelope opened and
// matched against the placement re-derived from the sealed seed, every ticket
// number matched against the committed display permutation. It is the record
// that the database held the committed pattern BEFORE sales opened, so a
// later disclosure proves the pattern was not altered during the sale rather
// than being the first time anyone looked.
//
// A failed walk is recorded too, with Error set, so a round that never sold
// still shows why.
type VerifyRecord struct {
	Kind               DrawKind      `json:"kind"`
	RoundID            int           `json:"roundId"`
	Outcome            VerifyOutcome `json:"outcome"`
	AlgorithmVersion   int           `json:"algorithmVersion,omitempty"`
	TotalTickets       int           `json:"totalTickets"`             // M, as configured and as committed
	PositionsWalked    int           `json:"positionsWalked"`          // rows read back (M on success)
	PrizePositions     int           `json:"prizePositions,omitempty"` // prize units found in the rows
	InputDigest        string        `json:"inputDigest"`              // allocation digest rebuilt from the rows
	RulesDigest        string        `json:"rulesDigest,omitempty"`
	DisplayDigest      string        `json:"displayDigest,omitempty"` // ticket-number digest rebuilt from the rows
	ChunkCount         int           `json:"chunkCount,omitempty"`    // committed mint chunks checked
	ChunksDigest       string        `json:"chunksDigest,omitempty"`  // digest over the ordered chunk digests
	ReproducedFromSeed bool          `json:"reproducedFromSeed"`      // rows compared with a fresh derivation from the sealed seed, not only with the digest
	Error              string        `json:"error,omitempty"`
	Verifier           string        `json:"verifier"` // program that walked the rows
	LibraryVersion     string        `json:"libraryVersion,omitempty"`
	AdminID            int           `json:"adminId,omitempty"` // 0 when queued by the round opening
	StartedAt          string        `json:"startedAt"`         // RFC3339
	FinishedAt         string        `json:"finishedAt"`        // RFC3339
}

// VerifyKey is the immudb key under which a mint verification is stored.
func VerifyKey(kind DrawKind, roundID int) string {
	return fmt.Sprintf("draw:verify:%s:%d", kind, roundID)
}

// DigestChunks folds an ordered list of mint-chunk digests (hex strings) into
// one digest, so a round's chunk ledger can be committed and checked as a
// single value. Each digest is framed by its length so the list cannot be
// re-split into the same bytes.
func DigestChunks(chunkDigests []string) string {
	return DigestStringsOrdered(chunkDigests)
}
