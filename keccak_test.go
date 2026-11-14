package netsec

import (
	"math/big"
	"testing"
)

func TestKeccak256Vectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"},
		{"abc", []byte("abc"), "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45"},
		{"long", []byte("The quick brown fox jumps over the lazy dog"),
			"4d741b6f1eb29cb2a9b9911c82f56fa8d73b04959d3d9d222895df6c0b28aa15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keccak256(tc.in)
			if hex := bytesToHex(got[:]); hex != tc.want {
				t.Fatalf("keccak256(%q) = %s, want %s", tc.name, hex, tc.want)
			}
		})
	}
}

func TestEIP1967Slots(t *testing.T) {
	// 官方 EIP-1967 规定的槽位常量，验证管理员/实现槽彼此独立且不可与顺序槽冲突。
	cases := []struct {
		name string
		got  *big.Int
		want string
	}{
		{"admin", AdminSlot, "b53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"},
		{"implementation", ImplementationSlot, "360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"},
		{"beacon", BeaconSlot, "a3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.got.Text(16); got != tc.want {
				t.Fatalf("%s slot = %s, want %s", tc.name, got, tc.want)
			}
			if seen[tc.want] {
				t.Fatalf("保留槽位 %s 重复", tc.want)
			}
			seen[tc.want] = true
		})
	}

	// 顺序分配的业务槽位从 0 开始，不可能等于任何 EIP-1967 哈希槽。
	for slot := int64(0); slot < 64; slot++ {
		if _, conflict := reservedSlotByName(big.NewInt(slot)); conflict {
			t.Fatalf("业务顺序槽 %d 与 EIP-1967 保留槽冲突", slot)
		}
	}
}
