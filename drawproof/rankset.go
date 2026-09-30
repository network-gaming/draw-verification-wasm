package drawproof

import "math/bits"

// rankSet is a set of taken positions over [1, totalTickets] with two
// operations: mark a position taken, and count the taken positions up to a
// position. It is a bitset with a Fenwick tree over blocks of 512 positions,
// so both are O(log M) and the whole structure is about M/8 bytes: 1.25 MB at
// ten million tickets, where the []bool it replaces was 10 MB per group.
type rankSet struct {
	words  []uint64 // bit p set when position p is taken (bit 0 unused)
	blocks []int32  // Fenwick tree, 1-based, over blocks of 8 words
	n      int
}

const rankBlockWords = 8 // 512 positions per block

func newRankSet(totalTickets int) *rankSet {
	nWords := totalTickets/64 + 1
	nBlocks := (nWords + rankBlockWords - 1) / rankBlockWords
	return &rankSet{words: make([]uint64, nWords), blocks: make([]int32, nBlocks+1), n: totalTickets}
}

func (r *rankSet) taken(p int) bool { return r.words[p>>6]&(1<<(uint(p)&63)) != 0 }

// take marks p taken. It is the caller's job not to take a position twice.
func (r *rankSet) take(p int) {
	r.words[p>>6] |= 1 << (uint(p) & 63)
	for b := p>>6/rankBlockWords + 1; b < len(r.blocks); b += b & -b {
		r.blocks[b]++
	}
}

// takenUpTo counts the taken positions in [1, p].
func (r *rankSet) takenUpTo(p int) int {
	if p <= 0 {
		return 0
	}
	if p > r.n {
		p = r.n
	}
	w := p >> 6
	block := w / rankBlockWords
	n := 0
	for b := block; b > 0; b -= b & -b {
		n += int(r.blocks[b])
	}
	for i := block * rankBlockWords; i < w; i++ {
		n += bits.OnesCount64(r.words[i])
	}
	// bits 0..p&63 of word w
	mask := uint64(1)<<(uint(p)&63+1) - 1
	if uint(p)&63 == 63 {
		mask = ^uint64(0)
	}
	n += bits.OnesCount64(r.words[w] & mask)
	return n
}

// freeIn counts the untaken positions in [lo, hi].
func (r *rankSet) freeIn(lo, hi int) int {
	if lo > hi {
		return 0
	}
	return (hi - lo + 1) - (r.takenUpTo(hi) - r.takenUpTo(lo-1))
}

// kthFree returns the position with 0-based rank k among the untaken
// positions inside s (ascending), and false when k is out of range.
func (r *rankSet) kthFree(s spans, k int) (int, bool) {
	if k < 0 {
		return 0, false
	}
	for _, x := range s {
		free := r.freeIn(x.lo, x.hi)
		if k >= free {
			k -= free
			continue
		}
		// Binary search the smallest p in [lo, hi] with free positions in
		// [lo, p] equal to k+1.
		base := r.takenUpTo(x.lo - 1)
		lo, hi := x.lo, x.hi
		for lo < hi {
			mid := lo + (hi-lo)/2
			freeUpTo := (mid - x.lo + 1) - (r.takenUpTo(mid) - base)
			if freeUpTo >= k+1 {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo, true
	}
	return 0, false
}
