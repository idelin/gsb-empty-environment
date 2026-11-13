package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestTransactionSizeRoundsUpToEightBytes(t *testing.T) {
	tests := []struct {
		name        string
		dataLength  int
		offsetCount int
		want        int
	}{
		{name: "empty", dataLength: 0, offsetCount: 0, want: 0},
		{name: "single byte", dataLength: 1, offsetCount: 0, want: 8},
		{name: "exact multiple", dataLength: 8, offsetCount: 0, want: 8},
		{name: "data plus offsets", dataLength: 5, offsetCount: 1, want: 16},
		{name: "offsets are eight bytes each", dataLength: 8, offsetCount: 2, want: 24},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TransactionSize(test.dataLength, test.offsetCount)
			if err != nil {
				t.Fatalf("TransactionSize() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("TransactionSize() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTransactionSizeRejectsNegativeInputs(t *testing.T) {
	if _, err := TransactionSize(-1, 0); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("TransactionSize(-1, 0) error = %v, want ErrInvalidSize", err)
	}
	if _, err := TransactionSize(0, -1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("TransactionSize(0, -1) error = %v, want ErrInvalidSize", err)
	}
}

func TestValidateOffsets(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		offsets []uint64
		wantErr error
	}{
		{
			name: "empty",
			data: nil,
		},
		{
			name:    "contiguous objects",
			data:    make([]byte, 72),
			offsets: []uint64{0, 24, 48},
		},
		{
			name:    "objects with gaps",
			data:    make([]byte, 80),
			offsets: []uint64{8, 40},
		},
		{
			name:    "misaligned offset",
			data:    make([]byte, 28),
			offsets: []uint64{1},
			wantErr: ErrOffsetAlignment,
		},
		{
			name:    "duplicate offset",
			data:    make([]byte, 48),
			offsets: []uint64{0, 0},
			wantErr: ErrOffsetOrder,
		},
		{
			name:    "decreasing offset",
			data:    make([]byte, 72),
			offsets: []uint64{24, 8},
			wantErr: ErrOffsetOrder,
		},
		{
			name:    "overlapping objects",
			data:    make([]byte, 48),
			offsets: []uint64{0, 8},
			wantErr: ErrObjectOverlap,
		},
		{
			name:    "one byte out of bounds",
			data:    make([]byte, 99),
			offsets: []uint64{76},
			wantErr: ErrObjectBounds,
		},
		{
			name:    "object ends exactly at data end",
			data:    make([]byte, 100),
			offsets: []uint64{76},
		},
		{
			name:    "object starts at data end",
			data:    make([]byte, 24),
			offsets: []uint64{24},
			wantErr: ErrObjectBounds,
		},
		{
			name:    "huge offset does not wrap",
			data:    make([]byte, 24),
			offsets: []uint64{math.MaxUint64 - 3},
			wantErr: ErrObjectBounds,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.data, test.offsets)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateSignedOffsetsRejectsNegative(t *testing.T) {
	if err := ValidateSignedOffsets(make([]byte, 24), []int{-4}); !errors.Is(err, ErrObjectBounds) {
		t.Fatalf("ValidateSignedOffsets() error = %v, want ErrObjectBounds", err)
	}
}
