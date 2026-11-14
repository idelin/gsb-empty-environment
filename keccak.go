package netsec

import "encoding/binary"

// keccakRoundConstants 是 keccak-f[1600] 的 24 轮轮常数。
var keccakRoundConstants = [24]uint64{
	0x0000000000000001, 0x0000000000008082, 0x800000000000808a, 0x8000000080008000,
	0x000000000000808b, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
	0x000000000000008a, 0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
	0x000000008000808b, 0x800000000000008b, 0x8000000000008089, 0x8000000000008003,
	0x8000000000008002, 0x8000000000000080, 0x000000000000800a, 0x800000008000000a,
	0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
}

// keccakF1600 对 25 个 uint64 道组成的状态执行 keccak-f[1600] 置换。
func keccakF1600(state *[25]uint64) {
	var bc [5]uint64
	var temp [25]uint64
	for round := 0; round < 24; round++ {
		// theta
		for x := 0; x < 5; x++ {
			bc[x] = state[x] ^ state[x+5] ^ state[x+10] ^ state[x+15] ^ state[x+20]
		}
		for x := 0; x < 5; x++ {
			theta := bc[(x+4)%5] ^ rotl64(bc[(x+1)%5], 1)
			for y := 0; y < 5; y++ {
				state[x+5*y] ^= theta
			}
		}
		// rho + pi：B[y, 2x+3y] = rotl(A[x,y], r(x,y))。
		// A 的道下标为 x+5y；B 的道下标为 y+5*(2x+3y)。
		temp[0] = state[0]
		for x := 0; x < 5; x++ {
			for y := 0; y < 5; y++ {
				if x == 0 && y == 0 {
					continue
				}
				shift := rhoOffset(x, y)
				targetLane := (y + 5*((2*x+3*y)%5)) % 25
				temp[targetLane] = rotl64(state[x+5*y], shift)
			}
		}
		// chi
		for y := 0; y < 5; y++ {
			base := 5 * y
			for x := 0; x < 5; x++ {
				state[base+x] = temp[base+x] ^ ((^temp[base+((x+1)%5)]) & temp[base+((x+2)%5)])
			}
		}
		// iota
		state[0] ^= keccakRoundConstants[round]
	}
}

func rotl64(value uint64, shift uint) uint64 {
	return (value << shift) | (value >> (64 - shift))
}

// rhoOffsetMatrix 按 [x][y] 索引给出 rho 步循环移位量。
var rhoOffsetMatrix = [5][5]uint{
	{0, 36, 3, 41, 18},
	{1, 44, 10, 45, 2},
	{62, 6, 43, 15, 61},
	{28, 55, 25, 21, 56},
	{27, 20, 39, 8, 14},
}

func rhoOffset(x, y int) uint {
	return rhoOffsetMatrix[x][y]
}

// keccak256 返回原始 Keccak-256 摘要（填充域 0x01），与以太坊使用的 keccak256 一致，
// 区别于 NIST SHA3-256（填充域 0x06）。
func keccak256(data []byte) [32]byte {
	const rate = 136 // 1088 bit
	var state [25]uint64

	offset := 0
	for len(data)-offset >= rate {
		block := data[offset : offset+rate]
		for i := 0; i < rate/8; i++ {
			state[i] ^= binary.LittleEndian.Uint64(block[i*8 : i*8+8])
		}
		keccakF1600(&state)
		offset += rate
	}
	data = data[offset:]

	var block [rate]byte
	copy(block[:], data)
	block[len(data)] = 0x01 // Keccak 多域填充：10*1
	block[rate-1] |= 0x80
	for i := 0; i < rate/8; i++ {
		state[i] ^= binary.LittleEndian.Uint64(block[i*8 : i*8+8])
	}
	keccakF1600(&state)

	var digest [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(digest[i*8:i*8+8], state[i])
	}
	return digest
}
