package lcc

import (
	"errors"
	"fmt"
	"io"
)

// RLE tag: bit 0 selects literal (0) or repeated byte (1); bits 1-7 store
// count-1, so each run contains 1..128 bytes.
func encodeRLE(input []byte) []byte {
	var output []byte
	for i := 0; i < len(input); {
		runEnd := i + 1
		for runEnd < len(input) && input[runEnd] == input[i] && runEnd-i < 128 {
			runEnd++
		}
		if runEnd-i >= 3 {
			output = append(output, byte(((runEnd-i-1)<<1)|1), input[i])
			i = runEnd
			continue
		}

		literalStart := i
		for i < len(input) && i-literalStart < 128 {
			nextRunEnd := i + 1
			for nextRunEnd < len(input) && input[nextRunEnd] == input[i] && nextRunEnd-i < 128 {
				nextRunEnd++
			}
			if nextRunEnd-i >= 3 {
				break
			}
			i = nextRunEnd
		}
		literalLen := i - literalStart
		output = append(output, byte((literalLen-1)<<1))
		output = append(output, input[literalStart:i]...)
	}
	return output
}

func decodeRLE(src []byte, dst io.Writer, declaredLen uint32) error {
	var written uint32
	for len(src) > 0 {
		tag := src[0]
		src = src[1:]
		count := uint32(tag>>1) + 1
		if count > declaredLen-written {
			return fmt.Errorf("decoded layer exceeds declared length %d", declaredLen)
		}

		if tag&1 == 0 {
			if uint64(len(src)) < uint64(count) {
				return errors.New("truncated literal run")
			}
			if _, err := dst.Write(src[:count]); err != nil {
				return err
			}
			src = src[count:]
		} else {
			if len(src) == 0 {
				return errors.New("truncated repeated run")
			}
			value := src[0]
			src = src[1:]
			var block [128]byte
			for i := range block {
				block[i] = value
			}
			if _, err := dst.Write(block[:count]); err != nil {
				return err
			}
		}
		written += count
	}
	if written != declaredLen {
		return fmt.Errorf("decoded length %d does not match declared length %d", written, declaredLen)
	}
	return nil
}

type sliceWriter struct {
	buf []byte
	pos int
}

func (w *sliceWriter) Write(p []byte) (int, error) {
	if len(p) > len(w.buf)-w.pos {
		return 0, errors.New("declared layer length exceeded")
	}
	copy(w.buf[w.pos:], p)
	w.pos += len(p)
	return len(p), nil
}

type budgetWriter struct {
	w         io.Writer
	remaining int64
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("output budget exceeded before writing %d bytes", len(p))
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}
