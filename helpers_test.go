package netsec

import (
	"context"
	"math"
	"testing"
)

func sameLayout(a, b Layout) bool {
	if len(a.Start) != len(b.Start) {
		return false
	}
	for i := range a.Start {
		if a.Start[i] != b.Start[i] {
			return false
		}
	}
	return true
}

// assertChiSquare 用卡方检验核对各类别计数是否与均匀期望相容。
// 阈值取自由度下约 1e-6 的尾概率，留出充足裕量避免随机抖动导致误报。
func assertChiSquare(t *testing.T, counts []int, expected float64) {
	t.Helper()
	stat := 0.0
	for _, count := range counts {
		diff := float64(count) - expected
		stat += diff * diff / expected
	}
	degrees := float64(len(counts) - 1)
	threshold := degrees + 4.5*math.Sqrt(2*degrees)
	if stat > threshold {
		t.Fatalf("chi-square stat %.2f exceeds threshold %.2f (counts=%v)", stat, threshold, counts)
	}
}

// lcg 是确定性的字节源，仅用于测试；不承担真实随机性。
type lcg struct {
	state uint64
}

func newLCG(seed uint64) *lcg {
	return &lcg{state: seed}
}

func (l *lcg) Read(p []byte) (int, error) {
	for i := range p {
		l.state = l.state*6364136223846793005 + 1442695040888963407
		p[i] = byte(l.state >> 33)
	}
	return len(p), nil
}

// cancelAfterReader 在读满 after 个字节后取消上下文并返回错误，
// 用于模拟抽样过程中随机流被取消。
type cancelAfterReader struct {
	after  int
	read   int
	cancel context.CancelFunc
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	if c.read >= c.after {
		c.cancel()
		return 0, context.Canceled
	}
	// 固定返回 0xFF：配合小 n 使首个取值落入拒绝区间，确保进入下一轮读取，
	// 从而稳定地在 after 个字节之后触发取消。
	p[0] = 0xFF
	c.read++
	return 1, nil
}

// smallSpecs 系统生成一批小域规格，覆盖不同域大小、区域数、尺寸、对齐
// 与保留区组合，包括必然产生零解的情形。
func smallSpecs() []Spec {
	specs := []Spec{}
	for domain := 1; domain <= 6; domain++ {
		for regions := 0; regions <= 3; regions++ {
			for _, reservedAt := range []int{-1, 0, domain / 2} {
				spec := Spec{DomainPages: domain}
				for i := 0; i < regions; i++ {
					size := 1 + (i+domain)%domain
					if size > domain {
						size = domain
					}
					align := 1 << uint((i+domain)%3)
					if align > domain {
						align = 1
					}
					spec.Regions = append(spec.Regions, Region{SizePages: size, AlignPages: align})
				}
				if reservedAt >= 0 {
					spec.Reserved = []Reservation{{StartPage: reservedAt, Pages: 1}}
				}
				specs = append(specs, spec)
			}
		}
	}
	return specs
}
