package netsec

import (
	"bytes"
	"fmt"
)

const (
	// MaxArgs is the largest number of host-supplied argument addresses.
	MaxArgs = 8
	// MaxMemoryBytes is the size of the protected memory window.
	MaxMemoryBytes = 16
	// MaxCount is the largest permitted output count.
	MaxCount = 4096
)

// Write describes one %K$hhn conversion that was executed.
type Write struct {
	// Arg is the one-based argument number K.
	Arg int
	// Address is the argument value supplied by the host.
	Address uintptr
	// Offset is the zero-based offset in the protected memory.
	Offset int
	// FormatOffset is the zero-based byte offset of the '%' in the format.
	FormatOffset int
	// Count is the output count at the time of the write.
	Count int
	// Value is the byte written, equal to Count modulo 256.
	Value byte
}

// ReplayResult is the outcome of a successful replay.
type ReplayResult struct {
	Writes      []Write
	FinalCount  int
	FinalMemory []byte
}

// FormatError reports a malformed or unsafe format at a specific byte. The
// replayer returns this error without modifying the caller's memory.
type FormatError struct {
	Offset int
	Reason string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("format byte %d: %s", e.Offset, e.Reason)
}

// Replay executes format against initial as though the protected memory began
// at address zero. Argument values are therefore byte offsets.
func Replay(format []byte, args []uintptr, initial []byte) (*ReplayResult, error) {
	return ReplayAt(0, format, args, initial)
}

// ReplayString is the string form of Replay.
func ReplayString(format string, args []uintptr, initial []byte) (*ReplayResult, error) {
	return ReplayAt(0, []byte(format), args, initial)
}

// ReplayAt executes only the format subset required by this exercise:
//
//   - bytes 32..126, other than '%', add one to the count;
//   - %% adds one to the count;
//   - %Nc adds N to the count, where N is positive with no leading zero;
//   - %K$hhn writes count mod 256 through argument K.
//
// K is one-based. A conversion that is malformed, unknown, out of range,
// overflows the count, or reuses an argument is rejected. Writes are staged in
// a private copy, so a failed replay never commits any memory write.
func ReplayAt(base uintptr, format []byte, args []uintptr, initial []byte) (*ReplayResult, error) {
	if len(args) > MaxArgs {
		return nil, fmt.Errorf("too many arguments: %d (maximum %d)", len(args), MaxArgs)
	}
	if len(initial) == 0 || len(initial) > MaxMemoryBytes {
		return nil, fmt.Errorf("memory size %d is outside 1..%d", len(initial), MaxMemoryBytes)
	}

	memory := bytes.Clone(initial)
	count := 0
	used := make(map[int]bool, len(args))
	var writes []Write

	for i := 0; i < len(format); {
		b := format[i]
		if b != '%' {
			if b < 32 || b > 126 {
				return nil, &FormatError{Offset: i, Reason: fmt.Sprintf("non-printable byte 0x%02x", b)}
			}
			if count == MaxCount {
				return nil, &FormatError{Offset: i, Reason: "output count would exceed 4096"}
			}
			count++
			i++
			continue
		}

		if i+1 >= len(format) {
			return nil, &FormatError{Offset: i, Reason: "incomplete conversion at end of format"}
		}
		if format[i+1] == '%' {
			if count == MaxCount {
				return nil, &FormatError{Offset: i, Reason: "output count would exceed 4096"}
			}
			count++
			i += 2
			continue
		}

		n, next, err := parsePositiveDecimal(format, i+1)
		if err != nil {
			return nil, &FormatError{Offset: i, Reason: err.Error()}
		}
		if next >= len(format) {
			return nil, &FormatError{Offset: i, Reason: "incomplete conversion"}
		}

		switch format[next] {
		case 'c':
			if n > MaxCount-count {
				return nil, &FormatError{Offset: i, Reason: "output count would exceed 4096"}
			}
			count += n
			i = next + 1

		case '$':
			if next+3 >= len(format) ||
				format[next+1] != 'h' ||
				format[next+2] != 'h' ||
				format[next+3] != 'n' {
				return nil, &FormatError{Offset: i, Reason: "unknown conversion (only %K$hhn is supported)"}
			}
			if n > len(args) {
				return nil, &FormatError{
					Offset: i,
					Reason: fmt.Sprintf("argument %d is out of range for %d arguments", n, len(args)),
				}
			}
			if used[n] {
				return nil, &FormatError{Offset: i, Reason: fmt.Sprintf("argument %d is written more than once", n)}
			}

			address := args[n-1]
			offset, ok := addressOffset(base, address, len(memory))
			if !ok {
				return nil, &FormatError{
					Offset: i,
					Reason: fmt.Sprintf("argument %d address 0x%x is outside the protected memory", n, address),
				}
			}

			value := byte(count & 0xff)
			memory[offset] = value
			used[n] = true
			writes = append(writes, Write{
				Arg:          n,
				Address:      address,
				Offset:       offset,
				FormatOffset: i,
				Count:        count,
				Value:        value,
			})
			i = next + 4

		default:
			return nil, &FormatError{Offset: i, Reason: "unknown conversion"}
		}
	}

	return &ReplayResult{
		Writes:      writes,
		FinalCount:  count,
		FinalMemory: memory,
	}, nil
}

// parsePositiveDecimal parses a positive decimal with no leading zero. Very
// large values are capped above MaxCount so parsing itself cannot overflow.
func parsePositiveDecimal(src []byte, start int) (value int, next int, err error) {
	if start >= len(src) {
		return 0, 0, fmt.Errorf("incomplete conversion")
	}
	if src[start] < '0' || src[start] > '9' {
		return 0, 0, fmt.Errorf("unknown conversion (expected decimal)")
	}
	if src[start] == '0' {
		return 0, 0, fmt.Errorf("number must be positive and have no leading zero")
	}

	const capped = MaxCount + 1
	value = 0
	i := start
	for i < len(src) && src[i] >= '0' && src[i] <= '9' {
		if value < capped {
			value = value*10 + int(src[i]-'0')
			if value > capped {
				value = capped
			}
		}
		i++
	}
	return value, i, nil
}

func addressOffset(base, address uintptr, size int) (int, bool) {
	if address < base {
		return 0, false
	}
	offset := address - base
	if offset >= uintptr(size) {
		return 0, false
	}
	return int(offset), true
}
