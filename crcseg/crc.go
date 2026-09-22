package crcseg

import "sync"

// Params 定义一个反射型（reflected, 与 IEEE/zlib CRC-32 同族）
// CRC-32 的三个外参：多项式、寄存器初值、末轮异或值。
//
// Polynomial 按“反射写法”给出，例如 IEEE 的 0xEDB88320。
type Params struct {
	Polynomial uint32
	Init       uint32
	FinalXOR   uint32
}

// 常见 CRC-32 参数集。
var (
	// IEEE 即 CRC-32/ISO-HDLC（zlib、gzip、zip、以太网使用）。
	IEEE = Params{Polynomial: 0xEDB88320, Init: 0xFFFFFFFF, FinalXOR: 0xFFFFFFFF}
	// Castagnoli 即 CRC-32C（iSCSI、SSE4.2）。
	Castagnoli = Params{Polynomial: 0x82F63B78, Init: 0xFFFFFFFF, FinalXOR: 0xFFFFFFFF}
)

// Valid 报告参数是否可用：多项式必须非零。
func (p Params) Valid() bool {
	return p.Polynomial != 0
}

// Empty 返回空输入的最终校验值（即 Init ^ FinalXOR）。
func (p Params) Empty() uint32 {
	return p.Init ^ p.FinalXOR
}

var (
	tableMu    sync.Mutex
	tableCache = map[uint32]*[256]uint32{}
)

func tableFor(poly uint32) *[256]uint32 {
	tableMu.Lock()
	defer tableMu.Unlock()
	if t, ok := tableCache[poly]; ok {
		return t
	}
	var t [256]uint32
	for i := 0; i < 256; i++ {
		crc := uint32(i)
		for j := 0; j < 8; j++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ poly
			} else {
				crc >>= 1
			}
		}
		t[i] = crc
	}
	tableCache[poly] = &t
	return &t
}

// update 以 crc 为当前寄存器状态吸收 data，返回更新后的寄存器状态。
// 注意：这里不施加 Init/FinalXOR，二者只在完整消息边界处理。
func update(p Params, crc uint32, data []byte) uint32 {
	t := tableFor(p.Polynomial)
	for _, b := range data {
		crc = (crc >> 8) ^ t[byte(crc)^b]
	}
	return crc
}

// Checksum 一次性计算 data 的最终校验值（含初末异或）。
func (p Params) Checksum(data []byte) (uint32, error) {
	if !p.Valid() {
		return 0, ErrInvalidParams
	}
	return update(p, p.Init, data) ^ p.FinalXOR, nil
}

// gf2Matrix 是 GF(2) 上的 32x32 比特矩阵，每个元素用一个 uint32 的
// 32 个比特表示一行：bit i 置 1 表示该行第 i 列为 1（LSB 第 0 列）。
type gf2Matrix [32]uint32

// gf2Mul 返回 GF(2) 矩阵乘积 c = a*b。
// 矩阵第 i 行存为 uint32，第 k 位表示该行第 k 列。
// 行向量 x（位掩码）乘矩阵 m 的结果：
//
//	(x*m)_j = parity(x AND col_j(m)),
//	col_j(m) 的第 k 位 = m[k] 的第 j 位。
//
// c 的第 i 行 = e_i * a * b（先取 a 的第 i 行作为行向量）。
func gf2Mul(a, b *gf2Matrix) gf2Matrix {
	var out gf2Matrix
	for k := uint(0); k < 32; k++ {
		rowA := a[k] // a 第 k 行，bit n = a[k][n]
		var rowC uint32
		for j := uint(0); j < 32; j++ {
			var colB uint32 // b 第 j 列，bit n = b[n][j]
			for n := uint(0); n < 32; n++ {
				if b[n]&(1<<j) != 0 {
					colB |= 1 << n
				}
			}
			rowC |= parity(rowA&colB) << j
		}
		out[k] = rowC
	}
	return out
}

// gf2Square 返回 a*a（GF(2) 上平方）。
func gf2Square(a *gf2Matrix) gf2Matrix {
	return gf2Mul(a, a)
}

// parity 返回 x 置位比特数的奇偶（1 = 奇数个 1）。
func parity(x uint32) uint32 {
	x ^= x >> 16
	x ^= x >> 8
	x ^= x >> 4
	x ^= x >> 2
	x ^= x >> 1
	return x & 1
}

// gf2Apply 计算 m*v：把列向量 v 左乘矩阵 m。
// 结果的第 n 位 = popcount(m[n] & v) 的奇偶。
func gf2Apply(m *gf2Matrix, v uint32) uint32 {
	var out uint32
	for n := uint(0); n < 32; n++ {
		out |= parity(m[n]&v) << n
	}
	return out
}

// zeroOperator 返回“吸收 len 个全零字节”在裸 CRC 寄存器上的线性算子。
// 一个零字节把寄存器 v 变为 (v>>8) ^ t[v&0xFF]；这是 GF(2) 线性变换，
// 且只依赖多项式、与数据内容无关——这正是可以按长度合成的原因。
func zeroOperator(p Params, length int64) gf2Matrix {
	t := tableFor(p.Polynomial)

	// 零字节更新 v -> (v>>8) ^ t[v&0xff]，是 GF(2) 线性映射。
	// 对每个输入基向量 e_n 直接算出输出列，按列存入矩阵：
	// M[k] 的第 n 位 = 输出位 k（M*e_n 的第 k 位）。
	var oneByte gf2Matrix
	for n := uint(0); n < 32; n++ {
		v := uint32(1) << n
		y := (v >> 8) ^ t[v&0xFF]
		for k := uint(0); k < 32; k++ {
			if y&(1<<k) != 0 {
				oneByte[k] |= 1 << n
			}
		}
	}

	// 二进求幂：length 个零字节 = oneByte^length。
	var result gf2Matrix
	for n := 0; n < 32; n++ {
		result[n] = uint32(1) << uint(n) // 单位矩阵
	}
	base := oneByte
	for length > 0 {
		if length&1 != 0 {
			result = gf2Mul(&result, &base)
		}
		base = gf2Square(&base)
		length >>= 1
	}
	return result
}

// combineRaw 已知 A、B 两段各自的“裸寄存器摘要”（未施加末异或），
// 返回拼接 A||B 的裸寄存器摘要。lenB 为 B 的字节长度。
//
// 对任意长度为 n 的 B 有：
//
//	raw(A||B) = Z_n(raw(A)) ^ shift(B)
//
// 其中 Z_n 为吸收 n 个零字节的线性算子，shift(B) 为把 B 当作
// 初始值 0 吸收得到的状态。对 B 单独计算时有：
//
//	raw(B) = Z_n(init) ^ shift(B)
//
// 因此 shift(B) = raw(B) ^ Z_n(init)。
func combineRaw(p Params, crcA, crcB uint32, lenB int64) uint32 {
	op := zeroOperator(p, lenB)
	return gf2Apply(&op, crcA) ^ crcB ^ gf2Apply(&op, p.Init)
}
