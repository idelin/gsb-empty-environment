package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// InstructionSize is the fixed size of an AArch64 instruction.
const InstructionSize = 4

var (
	// ErrInvalidConfig reports malformed text, alignment, or limit settings.
	ErrInvalidConfig = errors.New("netsec: invalid gadget scan config")
	// ErrBudgetExceeded reports that the scan cannot finish within Budget.
	ErrBudgetExceeded = errors.New("netsec: gadget scan exceeded caller budget")
)

const (
	retEncoding     uint32 = 0xd65f03c0
	brEncodingBase  uint32 = 0xd61f0000
	blrEncodingBase uint32 = 0xd63f0000
	branchRegMask   uint32 = 0xfffffc1f
)

// Config describes one bounded gadget scan.
type Config struct {
	// Text contains little-endian AArch64 text-section bytes. Its length must
	// be a multiple of InstructionSize.
	Text []byte

	// FunctionEntries contains legitimate function entry addresses. Entries
	// outside the text range are ignored; in-range entries must be aligned.
	FunctionEntries []uint64

	// MaxInstructions is K, the maximum number of instructions inspected
	// before each terminating instruction. The terminator is not counted in K.
	MaxInstructions int

	// Budget is the maximum number of four-byte instruction probes. The
	// initial text scan and every backward probe each consume one unit.
	Budget int

	// BaseAddress is the virtual address of Text[0]. When zero, addresses are
	// byte offsets relative to the start of Text.
	BaseAddress uint64
}

// Result reports distinct gadget starts and the lowest start address.
type Result struct {
	Count        int
	FirstAddress uint64
	Found        bool
}

// FindReturnGadgets scans backward from RET, BR, and BLR instructions within
// the caller-provided K and budget. It stops at direct branches and other
// control transfers, excludes starts that are function entries, deduplicates
// starts by address, and returns no partial result if Budget is exhausted.
func FindReturnGadgets(cfg Config) (Result, error) {
	if cfg.MaxInstructions < 0 {
		return Result{}, fmt.Errorf("%w: MaxInstructions must be non-negative", ErrInvalidConfig)
	}
	if cfg.Budget < 0 {
		return Result{}, fmt.Errorf("%w: Budget must be non-negative", ErrInvalidConfig)
	}
	if len(cfg.Text)%InstructionSize != 0 {
		return Result{}, fmt.Errorf("%w: text length %d is not a multiple of %d", ErrInvalidConfig, len(cfg.Text), InstructionSize)
	}
	if uint64(len(cfg.Text)) > math.MaxUint64-cfg.BaseAddress {
		return Result{}, fmt.Errorf("%w: base address %#x plus text length overflows", ErrInvalidConfig, cfg.BaseAddress)
	}

	entries, err := functionEntryWords(cfg.FunctionEntries, cfg.BaseAddress, uint64(len(cfg.Text)))
	if err != nil {
		return Result{}, err
	}

	scanner := gadgetScanner{
		text:    cfg.Text,
		base:    cfg.BaseAddress,
		entries: entries,
		starts:  make(map[int]struct{}),
		budget:  cfg.Budget,
	}

	words := len(cfg.Text) / InstructionSize
	for i := 0; i < words; i++ {
		if !scanner.spend() {
			return Result{}, budgetError(cfg.Budget, cfg.MaxInstructions)
		}

		insn := scanner.instruction(i)
		if !isTerminator(insn) {
			continue
		}

		scanner.addStart(i)

		maxBack := cfg.MaxInstructions
		if maxBack > i {
			maxBack = i
		}
		for j := i - 1; j >= i-maxBack; j-- {
			if !scanner.spend() {
				return Result{}, budgetError(cfg.Budget, cfg.MaxInstructions)
			}
			if isControlTransferBarrier(scanner.instruction(j)) {
				break
			}
			scanner.addStart(j)
		}
	}

	if scanner.count == 0 {
		return Result{}, nil
	}
	return Result{Count: scanner.count, FirstAddress: scanner.first, Found: true}, nil
}

func budgetError(budget, maxInstructions int) error {
	return fmt.Errorf("%w: budget %d exhausted (MaxInstructions=%d)", ErrBudgetExceeded, budget, maxInstructions)
}

type gadgetScanner struct {
	text    []byte
	base    uint64
	entries map[int]struct{}
	starts  map[int]struct{}
	spent   int
	budget  int
	count   int
	first   uint64
	found   bool
}

func (s *gadgetScanner) spend() bool {
	if s.spent >= s.budget {
		return false
	}
	s.spent++
	return true
}

func (s *gadgetScanner) instruction(word int) uint32 {
	offset := word * InstructionSize
	return binary.LittleEndian.Uint32(s.text[offset : offset+InstructionSize])
}

func (s *gadgetScanner) addStart(word int) {
	if _, ok := s.entries[word]; ok {
		return
	}
	if _, ok := s.starts[word]; ok {
		return
	}

	s.starts[word] = struct{}{}
	s.count++

	address := s.base + uint64(word)*InstructionSize
	if !s.found || address < s.first {
		s.first = address
		s.found = true
	}
}

func functionEntryWords(entries []uint64, base, textLength uint64) (map[int]struct{}, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	words := make(map[int]struct{}, len(entries))
	for _, address := range entries {
		if address < base {
			continue
		}
		delta := address - base
		if delta >= textLength {
			continue
		}
		if delta%InstructionSize != 0 {
			return nil, fmt.Errorf("%w: function entry %#x is not %d-byte aligned", ErrInvalidConfig, address, InstructionSize)
		}
		words[int(delta/InstructionSize)] = struct{}{}
	}
	return words, nil
}

func isTerminator(insn uint32) bool {
	if insn == retEncoding {
		return true
	}
	masked := insn & branchRegMask
	return masked == brEncodingBase || masked == blrEncodingBase
}

func isControlTransferBarrier(insn uint32) bool {
	return isDirectBranch(insn) || isTerminator(insn)
}

func isDirectBranch(insn uint32) bool {
	switch insn & 0xfc000000 {
	case 0x14000000, 0x94000000:
		return true
	}

	if insn&0xff000010 == 0x54000000 {
		return true
	}

	switch insn & 0x7e000000 {
	case 0x34000000, 0x36000000:
		return true
	default:
		return false
	}
}
