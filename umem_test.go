package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestCheckDescriptor(t *testing.T) {
	p := New()

	const (
		frame = 2048
		size  = 4096
	)
	aligned := UMEMConfig{Size: size, FrameSize: frame, Unaligned: false}
	unaligned := UMEMConfig{Size: size, FrameSize: frame, Unaligned: true}
	bigUnaligned := UMEMConfig{Size: 8192, FrameSize: frame, Unaligned: true}

	tests := []struct {
		name string
		umem UMEMConfig
		desc Descriptor
		want error
	}{
		// Aligned mode.
		{
			name: "aligned descriptor at frame zero",
			umem: aligned,
			desc: Descriptor{Addr: 0, Len: 100},
		},
		{
			name: "aligned descriptor fills second frame",
			umem: aligned,
			desc: Descriptor{Addr: frame, Len: frame},
		},
		{
			name: "aligned rejects unaligned address",
			umem: aligned,
			desc: Descriptor{Addr: 64, Len: 100},
			want: ErrUnalignedAddr,
		},
		{
			name: "aligned rejects address at UMEM end",
			umem: aligned,
			desc: Descriptor{Addr: size, Len: 1},
			want: ErrOutOfUMEM,
		},
		{
			name: "aligned rejects range extending past UMEM",
			umem: aligned,
			desc: Descriptor{Addr: frame, Len: frame + 1},
			want: ErrOutOfUMEM,
		},

		// Unaligned mode.
		{
			name: "unaligned accepts offset inside one frame",
			umem: unaligned,
			desc: Descriptor{Addr: 64, Len: 100},
		},
		{
			name: "unaligned accepts range ending on frame boundary",
			umem: unaligned,
			desc: Descriptor{Addr: 1024, Len: 1024},
		},
		{
			name: "unaligned rejects one byte over frame boundary",
			umem: unaligned,
			desc: Descriptor{Addr: 1024, Len: 1025},
			want: ErrCrossesChunk,
		},
		{
			name: "unaligned rejects descriptor spanning two frames",
			umem: bigUnaligned,
			desc: Descriptor{Addr: 1024, Len: 2000},
			want: ErrCrossesChunk,
		},
		{
			name: "unaligned rejects range past UMEM end",
			umem: bigUnaligned,
			desc: Descriptor{Addr: 8000, Len: 200},
			want: ErrOutOfUMEM,
		},

		// Shared overflow / shape checks.
		{
			name: "zero length rejected",
			umem: aligned,
			desc: Descriptor{Addr: 0, Len: 0},
			want: ErrZeroLength,
		},
		{
			name: "addr plus len overflow rejected",
			umem: unaligned,
			desc: Descriptor{Addr: math.MaxUint64 - 10, Len: 20},
			want: ErrAddrOverflow,
		},
		{
			name: "huge length wrapping past address rejected",
			umem: aligned,
			desc: Descriptor{Addr: 4000, Len: math.MaxUint64},
			want: ErrAddrOverflow,
		},
		{
			name: "zero-sized UMEM rejected",
			umem: UMEMConfig{Size: 0, FrameSize: frame},
			desc: Descriptor{Addr: 0, Len: 100},
			want: ErrInvalidUMEM,
		},
		{
			name: "frame larger than UMEM rejected",
			umem: UMEMConfig{Size: frame, FrameSize: size},
			desc: Descriptor{Addr: 0, Len: 100},
			want: ErrInvalidUMEM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.CheckDescriptor(tt.umem, tt.desc)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CheckDescriptor() = %v, want %v", err, tt.want)
			}
		})
	}
}
