// Package resample 提供供直播转码任务调用的流式多声道 PCM 重采样器。
//
// 重采样基于多相（polyphase）加窗 sinc 插值：滤波器按固定相位分辨率 P
// 预先离散化为 P 组子滤波器，流式处理时用整数累加器同时推进输入基准位置
// 与分数相位，无浮点漂移。跨块的历史样本与相位状态保留在 Resampler 内部，
// 因此任意分块方式得到的输出与整段一次处理逐样本（bit 级）一致。
package resample

import (
	"errors"
	"math"
)

// Window 选择 sinc 滤波器使用的窗函数。
type Window int

const (
	// WindowHann 为默认窗，旁瓣抑制与主瓣宽度的折中较好。
	WindowHann Window = iota
	WindowHamming
	WindowBlackman
	WindowBlackmanHarris
)

// Config 描述一次重采样任务的配置。
type Config struct {
	// InRate / OutRate 为输入/输出采样率（Hz），例如 44100、48000。
	InRate  int
	OutRate int
	// Channels 为交织 PCM 的声道数。
	Channels int
	// HalfLength 为 sinc 半长 L：每个相位的子滤波器含 2L 个抽头。
	// L 越大过渡带越窄、混叠抑制越好，CPU 与延迟越高。
	HalfLength int
	// CutoffHz 为低通截止频率（Hz）。0 表示取 0.85*min(InRate,OutRate)/2，
	// 预留 15% 过渡带，保证阻带在输出奈奎斯特频率处充分衰减。
	CutoffHz float64
	// Window 选择窗函数，零值为 WindowHann。
	Window Window
	// Phases 为多相表的分数相位分辨率 P。0 表示使用默认值 4096。
	// 相位量化误差约为 1/(2P) 个输入样本。
	Phases int
}

const (
	defaultHalfLength = 32
	defaultPhases     = 4096
	// cutoffGuard 是默认截止频率相对较低奈奎斯特频率的比例。
	cutoffGuard = 0.85
)

// Resampler 是有状态的流式重采样器。单个实例不可并发使用。
type Resampler struct {
	cfg Config

	// filter[p][j] 是第 p 个分数相位的第 j 个抽头（j 对应 t=j-L）。
	filter [][]float64

	// 每个声道保留当前流段自复位以来的全部输入样本，hist[ch][i]
	// 即绝对输入样本 i。分块处理与整段处理读取完全相同的样本序列，
	// 因而逐样本一致；Drain/Configure/SwitchRate 后清空。
	hist [][]float64
	// 已送入的输入样本总数（按帧计）。
	nIn int64
	// 整数相位累加器：输出第 n 个样本前 acc = n*P*InRate。
	acc int64

	incPerOut int64 // P*InRate，每次输出的累加增量
}

// New 按配置构造重采样器并预计算多相滤波器表。
func New(cfg Config) (*Resampler, error) {
	r := &Resampler{}
	if err := r.Configure(cfg); err != nil {
		return nil, err
	}
	return r, nil
}

// Configure 应用新配置并重建滤波器、清空所有流状态。
// 流中途切换采样率时应先调用 Drain 排空旧滤波器，再调用本方法。
func (r *Resampler) Configure(cfg Config) error {
	if cfg.InRate <= 0 || cfg.OutRate <= 0 {
		return errors.New("resample: InRate and OutRate must be positive")
	}
	if cfg.Channels <= 0 {
		return errors.New("resample: Channels must be positive")
	}
	if cfg.HalfLength == 0 {
		cfg.HalfLength = defaultHalfLength
	}
	if cfg.HalfLength < 0 {
		return errors.New("resample: HalfLength must be positive")
	}
	if cfg.Phases == 0 {
		cfg.Phases = defaultPhases
	}
	if cfg.Phases < 0 {
		return errors.New("resample: Phases must be positive")
	}

	nyquist := 0.5 * float64(minInt(cfg.InRate, cfg.OutRate))
	if cfg.CutoffHz == 0 {
		cfg.CutoffHz = cutoffGuard * nyquist
	}
	if cfg.CutoffHz <= 0 || cfg.CutoffHz > nyquist {
		return errors.New("resample: CutoffHz must lie in (0, min(InRate,OutRate)/2)")
	}

	r.cfg = cfg
	r.filter = buildPolyphase(cfg)
	r.hist = make([][]float64, cfg.Channels)
	r.nIn = 0
	r.acc = 0
	r.incPerOut = int64(cfg.Phases) * int64(cfg.InRate)
	return nil
}

// SwitchRate 排空当前滤波器的残余输出，然后切换到新配置。
// 返回的 tail 是旧采样率下最后一段输入对应的输出，调用方应先写出 tail
// 再继续向新重采样器送入新采样率的数据。
func (r *Resampler) SwitchRate(cfg Config) (tail []float64, err error) {
	tail = r.Drain()
	err = r.Configure(cfg)
	return tail, err
}

// buildPolyphase 构建 P 组、每组 2L 抽头的归一化加窗 sinc 子滤波器。
func buildPolyphase(cfg Config) [][]float64 {
	l := cfg.HalfLength
	p := cfg.Phases
	taps := 2 * l
	fs := float64(cfg.InRate)
	fc := cfg.CutoffHz

	table := make([][]float64, p)
	for phase := 0; phase < p; phase++ {
		frac := float64(phase) / float64(p)
		coeffs := make([]float64, taps)
		sum := 0.0
		for j := 0; j < taps; j++ {
			t := j - l // 抽头相对插值中心的整数偏移，范围 [-L, L-1]
			d := float64(t) - frac
			// 窗作用于固定抽头序号，不随分数相位移动；sinc 的零点
			// 位置则随 frac 平移。2L 个抽头均匀落在窗的两个零点之间，
			// 首尾抽头取非零小值，避免越界截断时系数和抵消为零。
			u := (float64(j) + 0.5) / float64(taps)
			w := 2 * fc / fs * sinc(2*fc/fs*d) * window(cfg.Window, u)
			coeffs[j] = w
			sum += w
		}
		// 逐相位归一化，保证任意分数相位下直流增益恒为 1。
		for j := range coeffs {
			coeffs[j] /= sum
		}
		table[phase] = coeffs
	}
	return table
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	v := math.Pi * x
	return math.Sin(v) / v
}

func window(w Window, u float64) float64 {
	switch w {
	case WindowHamming:
		return 0.54 - 0.46*math.Cos(2*math.Pi*u)
	case WindowBlackman:
		return 0.42 - 0.5*math.Cos(2*math.Pi*u) + 0.08*math.Cos(4*math.Pi*u)
	case WindowBlackmanHarris:
		return 0.35875 - 0.48829*math.Cos(2*math.Pi*u) +
			0.14128*math.Cos(4*math.Pi*u) - 0.01168*math.Cos(6*math.Pi*u)
	default: // WindowHann
		return 0.5 - 0.5*math.Cos(2*math.Pi*u)
	}
}

// Process 送入一块交织多声道 PCM（每 Channels 个值为一帧），
// 返回当前数据足以确定的全部输出帧（同样为交织布局）。
// 返回切片由本次调用独占，可长期保留。
func (r *Resampler) Process(in []float64) []float64 {
	ch := r.cfg.Channels
	if len(in)%ch != 0 {
		panic("resample: input sample count is not a multiple of Channels")
	}

	frames := len(in) / ch
	for f := 0; f < frames; f++ {
		for c := 0; c < ch; c++ {
			r.hist[c] = append(r.hist[c], in[f*ch+c])
		}
		r.nIn++
	}

	estOut := int(float64(frames)*float64(r.cfg.OutRate)/float64(r.cfg.InRate)) + 2*r.cfg.HalfLength + 1
	out := make([]float64, 0, (estOut+1)*ch)

	l := r.cfg.HalfLength
	p := int64(r.cfg.Phases)
	outRate := int64(r.cfg.OutRate)

	for {
		nextAcc := r.acc + r.incPerOut
		pp := nextAcc / outRate // 绝对分数位置，单位为 1/P 个输入样本
		k0 := pp / p            // 插值中心左侧的整数输入索引
		last := k0 + int64(l) - 1
		if last >= r.nIn {
			// 最后一个抽头尚无真实样本，等待后续输入块。
			break
		}
		r.acc = nextAcc // 确认可以产出后再提交相位状态
		phase := int(pp % p)
		coeffs := r.filter[phase]
		frame := make([]float64, ch)
		for c := 0; c < ch; c++ {
			frame[c] = r.convolve(c, k0, coeffs)
		}
		out = append(out, frame...)
	}

	return out
}

// convolve 计算单声道在插值中心 k0 处的滤波结果；
// 历史之外的索引（启动期与排空期）按零样本处理。
func (r *Resampler) convolve(ch int, k0 int64, coeffs []float64) float64 {
	l := r.cfg.HalfLength
	nIn := r.nIn
	acc, liveSum := 0.0, 0.0
	for j, w := range coeffs {
		idx := k0 + int64(j-l)
		if idx < 0 || idx >= nIn {
			continue
		}
		acc += w * r.hist[ch][idx]
		liveSum += w
	}
	if liveSum == 0 {
		return 0
	}
	return acc / liveSum
}

// Drain 在输入流结束或切换采样率前排空滤波器：对尾部不足的样本补零，
// 输出所有仍依赖至少一个真实样本的输出帧，然后复位流状态（保留配置与
// 滤波器表）。返回交织布局的尾部输出，可能为空。
func (r *Resampler) Drain() []float64 {
	ch := r.cfg.Channels
	l := r.cfg.HalfLength
	p := int64(r.cfg.Phases)
	outRate := int64(r.cfg.OutRate)

	out := make([]float64, 0, 2*l*ch)
	for {
		nextAcc := r.acc + r.incPerOut
		pp := nextAcc / outRate
		k0 := pp / p
		if k0-int64(l) >= r.nIn {
			// 最早的抽头也已越过全部真实样本，排空完成。
			break
		}
		r.acc = nextAcc
		phase := int(pp % p)
		coeffs := r.filter[phase]
		frame := make([]float64, ch)
		for c := 0; c < ch; c++ {
			frame[c] = r.convolve(c, k0, coeffs)
		}
		out = append(out, frame...)
	}

	for c := range r.hist {
		r.hist[c] = r.hist[c][:0]
	}
	r.nIn = 0
	r.acc = 0
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
