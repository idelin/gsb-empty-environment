package raster

import "runtime"

// DepthFunc 是可配置的深度比较函数。深度缓冲存储 NDC z（近小远大）。
type DepthFunc int

const (
	// DepthDefault 表示使用渲染器默认值（DepthLess）。
	DepthDefault DepthFunc = iota
	// DepthNever 总是失败。
	DepthNever
	// DepthLess 在片段深度严格小于已存深度时通过（默认）。
	DepthLess
	// DepthEqual 在片段深度等于已存深度时通过。
	DepthEqual
	// DepthLEqual 在片段深度小于等于已存深度时通过。
	DepthLEqual
	// DepthGreater 在片段深度严格大于已存深度时通过。
	DepthGreater
	// DepthNotEqual 在片段深度不等于已存深度时通过。
	DepthNotEqual
	// DepthGEqual 在片段深度大于等于已存深度时通过。
	DepthGEqual
	// DepthAlways 总是通过。
	DepthAlways
)

func (f DepthFunc) pass(fragment, stored float64) bool {
	switch f {
	case DepthNever:
		return false
	case DepthLess:
		return fragment < stored
	case DepthEqual:
		return fragment == stored
	case DepthLEqual:
		return fragment <= stored
	case DepthGreater:
		return fragment > stored
	case DepthNotEqual:
		return fragment != stored
	case DepthGEqual:
		return fragment >= stored
	case DepthAlways:
		return true
	default:
		return fragment < stored
	}
}

// Shader 对单个通过深度测试的片段着色。
//
// attr 是透视正确插值后的顶点属性，返回值写入颜色缓冲。实现必须是
// 无状态或自身并发安全的，因为并行模式下会从多个 goroutine 调用。
// nil 着色器直接把第一个属性作为颜色输出。
type Shader func(attr []float64) float64

// Config 控制渲染器行为。
type Config struct {
	// AttrCount 是每个顶点携带的属性数量，必须为正。
	AttrCount int
	// Depth 选择深度比较函数，零值 DepthDefault 等价于 DepthLess。
	Depth DepthFunc
	// DepthWrite 为 false 时只做深度测试，不回写深度。
	DepthWrite bool
	// DepthTest 为 true 时启用深度测试；零值 false 表示跳过测试，
	// 所有片段都通过并直接写入。
	DepthTest bool
	// TileSize 是并行图块的像素边长，<=0 时使用默认值 32。
	TileSize int
	// Workers 是并行 worker 数量，<=0 时取 GOMAXPROCS；1 表示串行。
	Workers int
}

// Renderer 串行/图块并行光栅化器。
type Renderer struct {
	cfg     Config
	shader  Shader
	workers int
}

type renderError string

func (e renderError) Error() string { return "raster: " + string(e) }

func errConfig(msg string) error { return renderError(msg) }

// New 创建渲染器并校验配置。
func New(cfg Config) (*Renderer, error) {
	if cfg.AttrCount <= 0 {
		return nil, errConfig("AttrCount must be positive")
	}
	if cfg.TileSize <= 0 {
		cfg.TileSize = 32
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
		if cfg.Workers < 1 {
			cfg.Workers = 1
		}
	}
	if cfg.Depth == DepthDefault {
		cfg.Depth = DepthLess
	}
	return &Renderer{cfg: cfg, workers: cfg.Workers}, nil
}

// edgeValue 返回有向边函数 (b-a) x (p-a) 的定点值。
func edgeValue(ax, ay, bx, by, px, py fixed) fixed {
	return (bx-ax)*(py-ay) - (by-ay)*(px-ax)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
