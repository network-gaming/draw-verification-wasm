// Package drawproof provides the deterministic, independently-verifiable core of
// Raffle Town's prize-draw mechanics.
//
// The selection of winners (main prize draw) and the allocation of instant-win
// prizes to ticket numbers are pure functions of (input set, seed). Given the
// published seed and input set, any third party can re-run these functions and
// reproduce the exact result without access to platform systems. Randomness is
// derived from an HMAC-SHA256 deterministic random bit generator (DRBG) so the
// algorithm is trivially re-implementable in any language.
//
// The seed itself is produced from a cryptographically secure source
// (crypto/rand) and is mixed with an external public randomness beacon (see the
// beacon sub-package) so that no party — including the platform — can pre-select
// a favourable outcome.
//
// This package performs NO I/O and has NO dependencies beyond the standard
// library. It is the trust anchor: the platform's draw paths and the standalone
// verifier both call the same functions here.
package drawproof

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strconv"
)

// SeedBytes is the length of a locally-generated seed.
const SeedBytes = 32

// drbg is a counter-mode HMAC-SHA256 deterministic random bit generator.
// block_i = HMAC-SHA256(seed, i) for i = 0,1,2,...; the 64-bit words are taken
// from the resulting keystream. It is fully deterministic in seed.
type drbg struct {
	seed    []byte
	counter uint64
	buf     [sha256.Size]byte
	bufLen  int // number of valid bytes remaining at the tail of buf
}

func newDRBG(seed []byte) *drbg {
	// Bind the seed length into a fixed key so callers can pass any length.
	key := sha256.Sum256(seed)
	return &drbg{seed: key[:]}
}

// refill computes the next keystream block.
func (d *drbg) refill() {
	mac := hmac.New(sha256.New, d.seed)
	var ctr [8]byte
	binary.BigEndian.PutUint64(ctr[:], d.counter)
	d.counter++
	mac.Write(ctr[:])
	sum := mac.Sum(nil)
	copy(d.buf[:], sum)
	d.bufLen = sha256.Size
}

// next64 returns the next uniformly-distributed 64-bit value.
func (d *drbg) next64() uint64 {
	if d.bufLen < 8 {
		d.refill()
	}
	start := sha256.Size - d.bufLen
	v := binary.BigEndian.Uint64(d.buf[start : start+8])
	d.bufLen -= 8
	return v
}

// intn returns a uniformly-distributed integer in [0, n) using rejection
// sampling to avoid modulo bias. n must be > 0.
func (d *drbg) intn(n int) int {
	if n <= 0 {
		return 0
	}
	un := uint64(n)
	// Largest multiple of n that fits in uint64; reject values at or above it.
	limit := (^uint64(0) / un) * un
	for {
		v := d.next64()
		if v < limit {
			return int(v % un)
		}
	}
}

// GenerateSeed returns a cryptographically-secure random seed.
func GenerateSeed() ([]byte, error) {
	b := make([]byte, SeedBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// MixExternalEntropy folds an external public randomness value (e.g. a drand
// beacon pulse) into a locally-generated seed: final = SHA256(local || beacon).
// With this, the platform cannot have known the beacon when committing the local
// seed, and cannot alter the local seed after committing its hash — so neither
// party can grind a favourable outcome.
func MixExternalEntropy(localSeed, beacon []byte) []byte {
	h := sha256.New()
	h.Write(localSeed)
	h.Write(beacon)
	return h.Sum(nil)
}

// HashSeed returns the hex SHA-256 of a seed — the value committed before a draw.
func HashSeed(seed []byte) string {
	sum := sha256.Sum256(seed)
	return hex.EncodeToString(sum[:])
}

// CanonicalOrder returns a sorted copy of the pool. Both the platform and an
// independent verifier order the eligible pool this way before running
// SelectWinners, so the reproduced winners do not depend on the (arbitrary) order
// in which the pool happens to be published or read from the database.
func CanonicalOrder(pool []string) []string {
	cp := make([]string, len(pool))
	copy(cp, pool)
	sort.Strings(cp)
	return cp
}

// SelectWinners deterministically selects `count` distinct entries from pool,
// in selection order, as a pure function of (pool, seed). It performs a partial
// Fisher–Yates shuffle so the result is an unbiased sample without replacement.
//
// pool is treated as an ordered list of entry identifiers; the caller is
// responsible for fixing (freezing) and publishing that order. If count exceeds
// len(pool), all entries are returned in selection order.
func SelectWinners(pool []string, count int, seed []byte) []string {
	n := len(pool)
	if n == 0 || count <= 0 {
		return nil
	}
	if count > n {
		count = n
	}
	work := make([]string, n)
	copy(work, pool)
	d := newDRBG(seed)
	winners := make([]string, 0, count)
	for i := 0; i < count; i++ {
		j := i + d.intn(n-i)
		work[i], work[j] = work[j], work[i]
		winners = append(winners, work[i])
	}
	return winners
}

// AllocateInstantPrizes deterministically assigns each prize unit to a distinct
// ticket number in [1, totalTickets], as a pure function of (totalTickets,
// units, seed). units is an ordered list of prize-unit identifiers (one entry
// per individual prize to be placed). The returned map is ticketNumber ->
// prize-unit identifier.
//
// Ticket numbers are drawn via a partial Fisher–Yates over [1, totalTickets],
// guaranteeing distinct placements without rejection loops.
func AllocateInstantPrizes(totalTickets int, units []string, seed []byte) map[int]string {
	out := make(map[int]string)
	if totalTickets <= 0 || len(units) == 0 {
		return out
	}
	count := len(units)
	if count > totalTickets {
		count = totalTickets
	}
	numbers := make([]int, totalTickets)
	for i := range numbers {
		numbers[i] = i + 1
	}
	d := newDRBG(seed)
	for i := 0; i < count; i++ {
		j := i + d.intn(totalTickets-i)
		numbers[i], numbers[j] = numbers[j], numbers[i]
		out[numbers[i]] = units[i]
	}
	return out
}

// DigestStringsSorted returns a canonical, order-independent SHA-256 digest of a
// set of strings (e.g. the frozen eligible pool). Each item is length-framed to
// avoid concatenation ambiguity, and items are sorted so the digest does not
// depend on input ordering.
func DigestStringsSorted(items []string) string {
	cp := make([]string, len(items))
	copy(cp, items)
	sort.Strings(cp)
	h := sha256.New()
	for _, s := range cp {
		writeFramed(h, s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DigestStringsOrdered returns a canonical SHA-256 digest that DOES depend on
// order — used for an ordered result set such as the winners list.
func DigestStringsOrdered(items []string) string {
	h := sha256.New()
	for _, s := range items {
		writeFramed(h, s)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// DigestAllocation returns a canonical SHA-256 digest of a ticketNumber -> prize
// allocation, independent of map iteration order (keys are sorted). This is the
// value committed before instant-win sales open.
func DigestAllocation(alloc map[int]string) string {
	keys := make([]int, 0, len(alloc))
	for k := range alloc {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	h := sha256.New()
	for _, k := range keys {
		writeFramed(h, strconv.Itoa(k))
		writeFramed(h, alloc[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeFramed writes len(s) as an 8-byte big-endian prefix followed by s, so the
// concatenation of framed values is unambiguous.
func writeFramed(h interface{ Write([]byte) (int, error) }, s string) {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(s)))
	_, _ = h.Write(l[:])
	_, _ = h.Write([]byte(s))
}
