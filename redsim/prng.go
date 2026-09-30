package redsim

// Source 是可注入的均匀随机位源。所有抽样都通过该接口完成，
// 测试可以注入确定性源或枚举源。
type Source interface {
	Uint64() uint64
}

// SplitMix64 是一个小型确定性 PRNG（仅标准库）。
type SplitMix64 struct {
	state uint64
}

func NewSplitMix64(seed uint64) *SplitMix64 { return &SplitMix64{state: seed} }

func (s *SplitMix64) Uint64() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Float64 把 64 位随机数映射到 [0,1)（取高 53 位）。
func Float64(v uint64) float64 { return float64(v>>11) * (1.0 / (1 << 53)) }

// Deriver 把一个主 seed 按事件身份分流成互相独立的随机流。
// 抽样结果只取决于“谁在抽”（kind+id），与调用顺序、重放、分块无关。
type Deriver struct{ seed uint64 }

func NewDeriver(seed uint64) Deriver { return Deriver{seed: seed} }

// For 为给定事件身份派生一条独立随机流。
func (d Deriver) For(kind string, id uint64) *SplitMix64 {
	h := uint64(1469598103934665603) // FNV-1a offset basis
	for i := 0; i < len(kind); i++ {
		h ^= uint64(kind[i])
		h *= 1099511628211
	}
	h ^= id
	h *= 1099511628211
	return NewSplitMix64(d.seed ^ h)
}
