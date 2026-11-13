package netsec

import (
	"errors"
	"math"
	"testing"
)

type mockChain struct {
	height  uint64
	outputs map[OutputIndex]OutputMeta
	spent   map[KeyImage]bool
	mempool map[KeyImage]bool
}

func (m *mockChain) CurrentHeight() uint64 { return m.height }

func (m *mockChain) GetOutput(index OutputIndex) (OutputMeta, bool) {
	meta, ok := m.outputs[index]
	return meta, ok
}

func (m *mockChain) IsSpent(image KeyImage) bool { return m.spent[image] }

func (m *mockChain) MempoolHasKeyImage(image KeyImage) bool { return m.mempool[image] }

func keyImage(b byte) KeyImage {
	var image KeyImage
	image[0] = b
	return image
}

// toOffsets 把绝对索引序列编码为 Monero 相对偏移列表。
func toOffsets(absolute ...uint64) []uint64 {
	offsets := make([]uint64, len(absolute))
	for i, index := range absolute {
		if i == 0 {
			offsets[i] = index
			continue
		}
		offsets[i] = index - absolute[i-1]
	}
	return offsets
}

func newMockChain(height uint64) *mockChain {
	return &mockChain{
		height:  height,
		outputs: make(map[OutputIndex]OutputMeta),
		spent:   make(map[KeyImage]bool),
		mempool: make(map[KeyImage]bool),
	}
}

// seedRing 在链上放置 16 个普通输出，绝对索引为 100..115，均已成熟。
func (m *mockChain) seedRing() []uint64 {
	indices := make([]uint64, 0, RequiredRingSize)
	for i := 0; i < RequiredRingSize; i++ {
		index := OutputIndex(100 + i)
		m.outputs[index] = OutputMeta{
			Index:       index,
			BlockHeight: m.height - NormalUnlockBlocks,
			Coinbase:    false,
		}
		indices = append(indices, uint64(index))
	}
	return indices
}

type harness struct {
	chain      *mockChain
	badOrder   map[KeyImage]bool
	badRingSig map[KeyImage]bool
	ringCalls  int
}

func newHarness(t *testing.T, height uint64) *harness {
	t.Helper()
	return &harness{
		chain:      newMockChain(height),
		badOrder:   make(map[KeyImage]bool),
		badRingSig: make(map[KeyImage]bool),
	}
}

func (h *harness) validator(t *testing.T) *Validator {
	t.Helper()
	v, err := NewValidator(
		h.chain,
		func(image KeyImage) bool { return !h.badOrder[image] },
		func(input *TxInput, ring []OutputMeta) bool {
			h.ringCalls++
			if len(ring) != RequiredRingSize {
				t.Fatalf("ring verifier received %d members, want %d", len(ring), RequiredRingSize)
			}
			return !h.badRingSig[input.KeyImage]
		},
	)
	if err != nil {
		t.Fatalf("NewValidator returned error: %v", err)
	}
	return v
}

func validInput(indices []uint64, image byte) TxInput {
	return TxInput{KeyImage: keyImage(image), KeyOffsets: toOffsets(indices...)}
}

func TestValidateRejectionCases(t *testing.T) {
	const height = uint64(100)

	tests := []struct {
		name      string
		setup     func(h *harness, indices []uint64) *Transaction
		want      error
		ringCalls int
	}{
		{
			name: "ring size 15 rejected",
			setup: func(h *harness, indices []uint64) *Transaction {
				for i := 0; i < RequiredRingSize-1; i++ {
					index := OutputIndex(100 + i)
					h.chain.outputs[index] = OutputMeta{Index: index, BlockHeight: 90}
				}
				return &Transaction{Inputs: []TxInput{
					{KeyImage: keyImage(1), KeyOffsets: toOffsets(indices[:15]...)},
				}}
			},
			want: ErrRingSize,
		},
		{
			name: "ring size 17 rejected",
			setup: func(h *harness, indices []uint64) *Transaction {
				for i := 0; i < RequiredRingSize+1; i++ {
					index := OutputIndex(100 + i)
					h.chain.outputs[index] = OutputMeta{Index: index, BlockHeight: 90}
				}
				extra := append(append([]uint64(nil), indices...), 116)
				return &Transaction{Inputs: []TxInput{
					{KeyImage: keyImage(1), KeyOffsets: toOffsets(extra...)},
				}}
			},
			want: ErrRingSize,
		},
		{
			name: "duplicate offset repeats a ring member",
			setup: func(h *harness, indices []uint64) *Transaction {
				offsets := toOffsets(indices...)
				offsets[5] = 0
				return &Transaction{Inputs: []TxInput{
					{KeyImage: keyImage(1), KeyOffsets: offsets},
				}}
			},
			want: ErrDuplicateRingMember,
		},
		{
			name: "normal output one block short of unlock",
			setup: func(h *harness, indices []uint64) *Transaction {
				locked := OutputIndex(indices[3])
				h.chain.outputs[locked] = OutputMeta{Index: locked, BlockHeight: height - NormalUnlockBlocks + 1}
				return &Transaction{Inputs: []TxInput{validInput(indices, 1)}}
			},
			want: ErrOutputLocked,
		},
		{
			name: "coinbase output one block short of 60",
			setup: func(h *harness, indices []uint64) *Transaction {
				locked := OutputIndex(indices[7])
				h.chain.outputs[locked] = OutputMeta{
					Index:       locked,
					BlockHeight: height - CoinbaseUnlockBlocks + 1,
					Coinbase:    true,
				}
				return &Transaction{Inputs: []TxInput{validInput(indices, 1)}}
			},
			want: ErrOutputLocked,
		},
		{
			name: "missing ring member output",
			setup: func(h *harness, indices []uint64) *Transaction {
				delete(h.chain.outputs, OutputIndex(indices[2]))
				return &Transaction{Inputs: []TxInput{validInput(indices, 1)}}
			},
			want: ErrOutputNotFound,
		},
		{
			name: "key image double spend from spent set",
			setup: func(h *harness, indices []uint64) *Transaction {
				image := keyImage(9)
				h.chain.spent[image] = true
				return &Transaction{Inputs: []TxInput{validInput(indices, 9)}}
			},
			want:      ErrKeyImageSpent,
			ringCalls: 0,
		},
		{
			name: "key image duplicated within transaction",
			setup: func(h *harness, indices []uint64) *Transaction {
				second := validInput(indices, 5)
				second.KeyImage = keyImage(1)
				return &Transaction{Inputs: []TxInput{validInput(indices, 1), second}}
			},
			want:      ErrKeyImageDuplicateInTx,
			ringCalls: 1,
		},
		{
			name: "key image already referenced in mempool",
			setup: func(h *harness, indices []uint64) *Transaction {
				image := keyImage(4)
				h.chain.mempool[image] = true
				return &Transaction{Inputs: []TxInput{validInput(indices, 4)}}
			},
			want:      ErrKeyImageInMempool,
			ringCalls: 0,
		},
		{
			name: "key image fails group order check",
			setup: func(h *harness, indices []uint64) *Transaction {
				h.badOrder[keyImage(2)] = true
				return &Transaction{Inputs: []TxInput{validInput(indices, 2)}}
			},
			want:      ErrKeyImageGroupOrder,
			ringCalls: 0,
		},
		{
			name: "ring signature verification fails",
			setup: func(h *harness, indices []uint64) *Transaction {
				h.badRingSig[keyImage(3)] = true
				return &Transaction{Inputs: []TxInput{validInput(indices, 3)}}
			},
			want:      ErrRingSignature,
			ringCalls: 1,
		},
		{
			name: "offset accumulation overflow",
			setup: func(h *harness, indices []uint64) *Transaction {
				offsets := make([]uint64, RequiredRingSize)
				offsets[0] = 1
				offsets[1] = math.MaxUint64
				return &Transaction{Inputs: []TxInput{
					{KeyImage: keyImage(1), KeyOffsets: offsets},
				}}
			},
			want: ErrOffsetOverflow,
		},
		{
			name: "no inputs rejected",
			setup: func(h *harness, indices []uint64) *Transaction {
				return &Transaction{}
			},
			want: ErrNoInputs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, height)
			indices := h.chain.seedRing()
			tx := tt.setup(h, indices)
			v := h.validator(t)

			err := v.Validate(tx)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Validate() error = %v, want error wrapping %v", err, tt.want)
			}
			if h.ringCalls != tt.ringCalls {
				t.Fatalf("ring verifier invoked %d times, want %d", h.ringCalls, tt.ringCalls)
			}
		})
	}
}

func TestValidateAcceptanceAndBoundaries(t *testing.T) {
	const height = uint64(100)

	t.Run("fully valid transaction passes", func(t *testing.T) {
		h := newHarness(t, height)
		indices := h.chain.seedRing()
		v := h.validator(t)

		tx := &Transaction{Inputs: []TxInput{validInput(indices, 1), validInput(indices, 2)}}
		if err := v.Validate(tx); err != nil {
			t.Fatalf("Validate() unexpected error: %v", err)
		}
		if h.ringCalls != 2 {
			t.Fatalf("ring verifier invoked %d times, want 2", h.ringCalls)
		}
	})

	t.Run("normal output unlocks exactly at 10 blocks", func(t *testing.T) {
		h := newHarness(t, height)
		indices := h.chain.seedRing()
		index := OutputIndex(indices[0])
		h.chain.outputs[index] = OutputMeta{Index: index, BlockHeight: height - NormalUnlockBlocks}

		if err := h.validator(t).Validate(&Transaction{Inputs: []TxInput{validInput(indices, 1)}}); err != nil {
			t.Fatalf("expected output at exact unlock boundary to pass, got %v", err)
		}
	})

	t.Run("coinbase output unlocks exactly at 60 blocks", func(t *testing.T) {
		h := newHarness(t, height)
		indices := h.chain.seedRing()
		index := OutputIndex(indices[0])
		h.chain.outputs[index] = OutputMeta{
			Index:       index,
			BlockHeight: height - CoinbaseUnlockBlocks,
			Coinbase:    true,
		}

		if err := h.validator(t).Validate(&Transaction{Inputs: []TxInput{validInput(indices, 1)}}); err != nil {
			t.Fatalf("expected coinbase at exact unlock boundary to pass, got %v", err)
		}
	})

	t.Run("nil transaction rejected", func(t *testing.T) {
		h := newHarness(t, height)
		if err := h.validator(t).Validate(nil); !errors.Is(err, ErrNoInputs) {
			t.Fatalf("Validate(nil) = %v, want %v", err, ErrNoInputs)
		}
	})
}

func TestNewValidatorNilDependencies(t *testing.T) {
	chain := newMockChain(1)
	order := func(KeyImage) bool { return true }
	ring := func(*TxInput, []OutputMeta) bool { return true }

	if _, err := NewValidator(nil, order, ring); !errors.Is(err, ErrNilDependency) {
		t.Fatalf("nil chain: got %v, want %v", err, ErrNilDependency)
	}
	if _, err := NewValidator(chain, nil, ring); !errors.Is(err, ErrNilDependency) {
		t.Fatalf("nil order checker: got %v, want %v", err, ErrNilDependency)
	}
	if _, err := NewValidator(chain, order, nil); !errors.Is(err, ErrNilDependency) {
		t.Fatalf("nil ring verifier: got %v, want %v", err, ErrNilDependency)
	}
}

func TestResolveAbsoluteOffsets(t *testing.T) {
	absolute, err := resolveAbsoluteOffsets([]uint64{100, 5, 3, 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []OutputIndex{100, 105, 108, 128}
	for i := range want {
		if absolute[i] != want[i] {
			t.Fatalf("position %d = %d, want %d", i, absolute[i], want[i])
		}
	}
}
