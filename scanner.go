package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// ErrRepairBudgetExhausted is returned when the image keeps changing and the
// scan cannot assemble a consistent snapshot within the configured budget.
var ErrRepairBudgetExhausted = errors.New("netsec: repair budget exhausted before a consistent snapshot")

// Options tunes how much repair work a scan may do after the first pass.
type Options struct {
	// RepairBudget caps the total number of page content re-reads allowed
	// after the initial full pass. Zero means no repair is permitted.
	RepairBudget int
	// MaxRounds caps the number of repair rounds as a backstop against
	// pathological sources. Values <= 0 select the default of 16.
	MaxRounds int
}

// Result describes the outcome of a scan. A digest tree is committed only
// when every page in it is provably from one single image generation.
type Result struct {
	// Committed is true only if a consistent snapshot was assembled and the
	// digest tree was committed.
	Committed bool
	// Root is the Merkle root over the committed pages.
	Root [sha256.Size]byte
	// CommitGen is the image commit generation the snapshot belongs to.
	CommitGen uint64
	// Pages is the number of pages in the committed tree.
	Pages int
	// Rounds is the number of repair rounds that were needed.
	Rounds int
	// PagesReRead is the number of page content re-reads spent on repairs.
	PagesReRead int
}

// Scanner builds a tamper-evident digest tree over a live process image
// without ever mixing pages from different modification generations.
type Scanner struct {
	src PageSource
	opt Options
}

// NewScanner returns a Scanner reading from src.
func NewScanner(src PageSource, opt Options) *Scanner {
	if opt.MaxRounds <= 0 {
		opt.MaxRounds = 16
	}
	if opt.RepairBudget < 0 {
		opt.RepairBudget = 0
	}
	return &Scanner{src: src, opt: opt}
}

type pageState struct {
	content  []byte
	writeGen uint64
}

// Scan reads the image and commits a digest tree only for a consistent
// snapshot: the commit generation observed before and after the reads must
// match, and every page must be provably untouched since it was read. If the
// image changed mid-scan, only the affected pages (changed write generation,
// unmapped, newly mapped) are re-read, bounded by the repair budget.
func (s *Scanner) Scan() (Result, error) {
	var res Result
	states := make(map[uint64]pageState)

	gen0 := s.src.CommitGen()
	order, err := s.src.ListPages()
	if err != nil {
		return res, err
	}
	incomplete := false
	for _, addr := range order {
		data, gen, err := s.src.ReadPage(addr)
		if errors.Is(err, ErrPageUnmapped) {
			incomplete = true
			continue
		}
		if err != nil {
			return res, err
		}
		states[addr] = pageState{content: data, writeGen: gen}
	}

	budget := s.opt.RepairBudget
	for round := 0; ; round++ {
		gen1 := s.src.CommitGen()
		if !incomplete && gen0 == gen1 && coversAll(states, order) {
			res.Committed = true
			res.Root = merkleRoot(order, states)
			res.CommitGen = gen0
			res.Pages = len(order)
			res.Rounds = round
			return res, nil
		}
		if round >= s.opt.MaxRounds {
			res.Rounds = round
			return res, ErrRepairBudgetExhausted
		}

		// Repair round: work inside a fresh generation window and touch
		// only affected pages.
		gen0 = s.src.CommitGen()
		cur, err := s.src.ListPages()
		if err != nil {
			return res, err
		}
		curSet := make(map[uint64]bool, len(cur))
		for _, addr := range cur {
			curSet[addr] = true
		}
		for addr := range states {
			if !curSet[addr] {
				delete(states, addr)
			}
		}

		var affected []uint64
		incomplete = false
		for _, addr := range cur {
			st, ok := states[addr]
			if !ok {
				affected = append(affected, addr)
				continue
			}
			gen, err := s.src.PageWriteGen(addr)
			switch {
			case errors.Is(err, ErrPageUnmapped):
				delete(states, addr)
				incomplete = true
			case err != nil:
				return res, err
			case gen != st.writeGen:
				affected = append(affected, addr)
			}
		}

		if len(affected) > budget {
			res.Rounds = round + 1
			return res, ErrRepairBudgetExhausted
		}
		budget -= len(affected)
		for _, addr := range affected {
			data, gen, err := s.src.ReadPage(addr)
			if errors.Is(err, ErrPageUnmapped) {
				delete(states, addr)
				incomplete = true
				continue
			}
			if err != nil {
				return res, err
			}
			states[addr] = pageState{content: data, writeGen: gen}
			res.PagesReRead++
		}
		order = cur
		for _, addr := range cur {
			if _, ok := states[addr]; !ok {
				incomplete = true
			}
		}
		res.Rounds = round + 1
	}
}

func coversAll(states map[uint64]pageState, order []uint64) bool {
	for _, addr := range order {
		if _, ok := states[addr]; !ok {
			return false
		}
	}
	return true
}

func leafDigest(addr uint64, content []byte) [sha256.Size]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], addr)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(len(content)))
	h.Write(buf[:])
	h.Write(content)
	var sum [sha256.Size]byte
	h.Sum(sum[:0])
	return sum
}

// merkleRoot builds the digest tree over the pages in the given order. Odd
// levels duplicate the last node; the empty image commits to sha256(nil).
func merkleRoot(order []uint64, states map[uint64]pageState) [sha256.Size]byte {
	if len(order) == 0 {
		return sha256.Sum256(nil)
	}
	level := make([][sha256.Size]byte, 0, len(order))
	for _, addr := range order {
		level = append(level, leafDigest(addr, states[addr].content))
	}
	for len(level) > 1 {
		next := make([][sha256.Size]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			left := level[i]
			right := left
			if i+1 < len(level) {
				right = level[i+1]
			}
			var pair [2 * sha256.Size]byte
			copy(pair[:sha256.Size], left[:])
			copy(pair[sha256.Size:], right[:])
			next = append(next, sha256.Sum256(pair[:]))
		}
		level = next
	}
	return level[0]
}
