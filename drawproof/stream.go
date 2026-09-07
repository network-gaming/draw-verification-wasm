package drawproof

// Stream exposes the raw, unscaled DRBG keystream for statistical testing (the
// GLI data-collection tool). It is the same generator the draw and allocation
// functions consume, instantiated from the same seed derivation, so what a lab
// tests is byte-for-byte what production uses.
type Stream struct{ d *drbg }

// NewStream returns a raw stream over the HMAC-SHA256 counter-mode DRBG keyed
// by SHA-256(seed).
func NewStream(seed []byte) *Stream { return &Stream{d: newDRBG(seed)} }

// Uint64 returns the next unscaled 64-bit word.
func (s *Stream) Uint64() uint64 { return s.d.next64() }

// Intn returns a uniformly distributed integer in [0, n) via the production
// rejection-sampling scaler.
func (s *Stream) Intn(n int) int { return s.d.intn(n) }
