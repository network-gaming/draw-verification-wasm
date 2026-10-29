package drawproof

import (
	"fmt"
	"strconv"
)

// AllocationFeed collects an Instant Wins v2 placement published as a file
// (Bundle.AllocationFile): one prize per line, the sale position, whitespace,
// then the prize-unit reference to the end of the line. Lines are fed in any
// order from a file or a stream, as positions (int32) and reference indexes
// (uint16), and turned into a Placement once the ticket population is known
// from the bundle's commit. A million prizes are six megabytes here against
// the hundreds of megabytes of a string-keyed map.
type AllocationFeed struct {
	pos  []int32
	ref  []uint16
	refs []string // index 0 unused, as in Placement
}

// Feed parses whole lines from text and appends them. Blank lines are
// skipped. Text must not end mid-line unless the caller carries the remainder
// over to the next call.
func (f *AllocationFeed) Feed(text string) error {
	if f.refs == nil {
		f.refs = []string{""}
	}
	start := 0
	for start < len(text) {
		end := start
		for end < len(text) && text[end] != '\n' {
			end++
		}
		line := text[start:end]
		start = end + 1
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if err := f.feedLine(line); err != nil {
			return err
		}
	}
	return nil
}

func (f *AllocationFeed) feedLine(line string) error {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if i == len(line) {
		return nil // blank
	}
	n, digits := 0, 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		n = n*10 + int(line[i]-'0')
		if n > 1<<31-1 {
			return fmt.Errorf("placement position exceeds int32 in line %q", line)
		}
		i++
		digits++
	}
	if digits == 0 {
		return fmt.Errorf("placement line does not start with a position: %q", line)
	}
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j == i || j == len(line) {
		return fmt.Errorf("placement line has no prize reference: %q", line)
	}
	ref := line[j:]
	for k := len(ref); k > 0 && (ref[k-1] == ' ' || ref[k-1] == '\t'); k-- {
		ref = ref[:k-1]
	}
	idx, err := f.refIndex(ref)
	if err != nil {
		return err
	}
	f.pos = append(f.pos, int32(n))
	f.ref = append(f.ref, idx)
	return nil
}

func (f *AllocationFeed) refIndex(ref string) (uint16, error) {
	for i := 1; i < len(f.refs); i++ {
		if f.refs[i] == ref {
			return uint16(i), nil
		}
	}
	if len(f.refs) > maxPlacementRefs {
		return 0, fmt.Errorf("placement holds more than %d distinct unit references", maxPlacementRefs)
	}
	f.refs = append(f.refs, ref)
	return uint16(len(f.refs) - 1), nil
}

// Len is the number of prizes fed so far.
func (f *AllocationFeed) Len() int { return len(f.pos) }

// Placement builds the placement over positions 1..totalTickets. A position
// outside that range, or fed twice, is an error: the file is not the
// placement it claims to be.
func (f *AllocationFeed) Placement(totalTickets int) (*Placement, error) {
	p := NewPlacement(totalTickets)
	for i, pos := range f.pos {
		if pos < 1 || int(pos) > totalTickets {
			return nil, fmt.Errorf("placement position %d outside 1..%d", pos, totalTickets)
		}
		if p.byPos[pos] != 0 {
			return nil, fmt.Errorf("placement position %d listed twice", pos)
		}
		idx, err := p.refIndex(f.refs[f.ref[i]])
		if err != nil {
			return nil, err
		}
		p.set(int(pos), idx)
	}
	return p, nil
}

// Map returns the fed prizes as position -> reference, for small files and
// tests. Later lines for a repeated position win.
func (f *AllocationFeed) Map() map[int]string {
	out := make(map[int]string, len(f.pos))
	for i, pos := range f.pos {
		out[int(pos)] = f.refs[f.ref[i]]
	}
	return out
}

// AllocationLine formats one prize for an allocation file: the position, a
// space, the reference, and a newline.
func AllocationLine(position int, ref string) string {
	return strconv.Itoa(position) + " " + ref + "\n"
}
