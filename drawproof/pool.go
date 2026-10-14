package drawproof

import (
	"fmt"
	"sort"
	"strconv"
)

// NumericPool holds a pool of decimal entry identifiers (ticket numbers) as
// int32 values: 40 MB for ten million entries against a quarter of a gigabyte
// of strings. Entries are fed in any order from a file or a stream and sorted
// once into canonical (byte-wise decimal string) order, after which At and
// Verify serve VerifyMainDrawIndexed.
type NumericPool struct {
	entries []int32
	sorted  bool
}

// Feed parses whitespace-separated decimal entries from text and appends them.
// Text may end mid-number only if the caller carries the remainder over; use
// FeedLines for chunked input.
func (p *NumericPool) Feed(text string) error {
	n, have := 0, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= '0' && c <= '9':
			n = n*10 + int(c-'0')
			if n > 1<<31-1 {
				return fmt.Errorf("pool entry exceeds int32 at byte %d", i)
			}
			have = true
		case c == '\n' || c == '\r' || c == ' ' || c == '\t' || c == ',':
			if have {
				p.entries = append(p.entries, int32(n))
				n, have = 0, false
			}
		default:
			return fmt.Errorf("pool contains a non-numeric entry near byte %d", i)
		}
	}
	if have {
		p.entries = append(p.entries, int32(n))
	}
	p.sorted = false
	return nil
}

// Len is the number of entries fed so far.
func (p *NumericPool) Len() int { return len(p.entries) }

// Sort orders the entries canonically: by the byte-wise order of their decimal
// strings, which is the order CanonicalOrder and DigestStringsSorted use.
func (p *NumericPool) Sort() {
	if p.sorted {
		return
	}
	sort.Slice(p.entries, func(i, j int) bool { return lessDecimal(p.entries[i], p.entries[j]) })
	p.sorted = true
}

// At returns the i-th entry in canonical order as its decimal string.
func (p *NumericPool) At(i int) string {
	p.Sort()
	return strconv.Itoa(int(p.entries[i]))
}

// Digest is DigestStringsSorted over the pool.
func (p *NumericPool) Digest() string {
	p.Sort()
	h := newFramedDigest()
	for _, e := range p.entries {
		h.add(strconv.Itoa(int(e)))
	}
	return h.sum()
}

// Verify runs VerifyMainDrawIndexed over the pool.
func (p *NumericPool) Verify(commit CommitRecord, reveal RevealRecord) VerifyResult {
	p.Sort()
	return VerifyMainDrawIndexed(len(p.entries), p.At, commit, reveal)
}

// lessDecimal compares two non-negative ints by the byte-wise order of their
// decimal strings without allocating them.
func lessDecimal(a, b int32) bool {
	var da, db [11]byte
	sa := appendDecimal(da[:0], a)
	sb := appendDecimal(db[:0], b)
	return string(sa) < string(sb)
}

func appendDecimal(buf []byte, v int32) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	var tmp [11]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(buf, tmp[i:]...)
}
