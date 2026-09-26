package rlebits

import "testing"

func TestExhaustiveDelete(t *testing.T) {
	for n := 1; n <= 9; n++ {
		for mask := 0; mask < (1 << n); mask++ {
			bits := make([]byte, n)
			for i := 0; i < n; i++ {
				bits[i] = byte((mask >> i) & 1)
			}
			for l := 0; l < n; l++ {
				for r := l + 1; r <= n; r++ {
					b := FromBits(bits)
					m := append([]byte{}, bits...)
					b.Delete(uint64(l), uint64(r))
					m = append(m[:l], m[r:]...)
					got := b.Expand()
					if len(got) != len(m) {
						t.Fatalf("n=%d mask=%b del[%d,%d): len got %d want %d", n, mask, l, r, len(got), len(m))
					}
					for i := range got {
						if got[i] != m[i] {
							t.Fatalf("n=%d bits=%b del[%d,%d): got %v want %v", n, mask, l, r, got, m)
						}
					}
					for _, rl := range b.runs {
						if rl == 0 {
							t.Fatalf("n=%d bits=%b del[%d,%d): zero run", n, mask, l, r)
						}
					}
				}
			}
		}
	}
}

func TestExhaustiveFlip(t *testing.T) {
	for n := 1; n <= 9; n++ {
		for mask := 0; mask < (1 << n); mask++ {
			bits := make([]byte, n)
			for i := 0; i < n; i++ {
				bits[i] = byte((mask >> i) & 1)
			}
			for l := 0; l < n; l++ {
				for r := l + 1; r <= n; r++ {
					b := FromBits(bits)
					m := append([]byte{}, bits...)
					b.Flip(uint64(l), uint64(r))
					for i := l; i < r; i++ {
						m[i] ^= 1
					}
					got := b.Expand()
					for i := range got {
						if got[i] != m[i] {
							t.Fatalf("n=%d bits=%b flip[%d,%d): got %v want %v", n, mask, l, r, got, m)
						}
					}
				}
			}
		}
	}
}

func TestExhaustiveInsert(t *testing.T) {
	for n := 0; n <= 8; n++ {
		for mask := 0; mask < (1 << n); mask++ {
			bits := make([]byte, n)
			for i := 0; i < n; i++ {
				bits[i] = byte((mask >> i) & 1)
			}
			for pos := 0; pos <= n; pos++ {
				for v := byte(0); v <= 1; v++ {
					for c := uint64(1); c <= 3; c++ {
						b := FromBits(bits)
						m := append([]byte{}, bits...)
						b.Insert(uint64(pos), v, c)
						tail := append([]byte{}, m[pos:]...)
						m = m[:pos]
						for k := uint64(0); k < c; k++ {
							m = append(m, v)
						}
						m = append(m, tail...)
						got := b.Expand()
						if len(got) != len(m) {
							t.Fatalf("n=%d bits=%b ins(%d,%d,%d): len got %d want %d", n, mask, pos, v, c, len(got), len(m))
						}
						for i := range got {
							if got[i] != m[i] {
								t.Fatalf("n=%d bits=%b ins(%d,%d,%d): got %v want %v", n, mask, pos, v, c, got, m)
							}
						}
						for _, rl := range b.runs {
							if rl == 0 {
								t.Fatalf("n=%d bits=%b ins(%d,%d,%d): zero run", n, mask, pos, v, c)
							}
						}
					}
				}
			}
		}
	}
}
