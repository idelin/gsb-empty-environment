package resample

import "math"

// SweepReport 是正弦扫频法对重采样质量的测量结果，所有量均为分贝，
// 数值越负越好（误差/泄漏能量相对于参考信号能量的比值）。
type SweepReport struct {
	InRate  int
	OutRate int

	// PassbandErrorDB 通带内输出与理想重采样信号的 RMS 误差（dB）。
	PassbandErrorDB float64
	// AliasSuppressionDB 下采样时输出奈奎斯特以上频率的混叠泄漏（dB）；
	// 上采样场景下该字段为 NaN。
	AliasSuppressionDB float64
	// ImageSuppressionDB 上采样时频谱镜像落入输出奈奎斯特内的能量（dB）；
	// 下采样场景下该字段为 NaN。
	ImageSuppressionDB float64
}

// LogSweep 生成持续 durationSec 秒的单声道对数正弦扫频，
// 瞬时频率从 f0 指数扫到 f1，幅度为 1。
func LogSweep(rate int, durationSec float64, f0, f1 float64) []float64 {
	n := int(float64(rate)*durationSec + 0.5)
	out := make([]float64, n)
	k := math.Log(f1 / f0)
	phase := 0.0
	for i := 0; i < n; i++ {
		out[i] = math.Sin(phase)
		f := f0 * math.Exp(k*float64(i)/float64(n))
		phase += 2 * math.Pi * f / float64(rate)
		if phase > 2*math.Pi {
			phase -= 2 * math.Pi
		}
	}
	return out
}

// MeasureSweep 用正弦扫频驱动一个按 cfg 配置的重采样器并测量质量。
// durationSec 控制扫频时长；测量内部自建重采样器，不影响调用方状态。
func MeasureSweep(cfg Config, durationSec float64) (SweepReport, error) {
	cfg.Channels = 1
	r, err := New(cfg)
	if err != nil {
		return SweepReport{}, err
	}

	f0 := 20.0
	inNyq := float64(cfg.InRate) / 2
	in := LogSweep(cfg.InRate, durationSec, f0, 0.97*inNyq)

	// 只统计正常流式输出，Drain 的补零尾段不参与质量评估。
	out := r.Process(in)

	kSweep := math.Log(0.97 * inNyq / f0)
	instF := func(n float64) float64 {
		return f0 * math.Exp(kSweep*n/float64(len(in)))
	}

	report := SweepReport{
		InRate:             cfg.InRate,
		OutRate:            cfg.OutRate,
		AliasSuppressionDB: math.NaN(),
		ImageSuppressionDB: math.NaN(),
	}
	report.PassbandErrorDB = measurePassband(out, in, cfg, instF)

	if cfg.OutRate < cfg.InRate {
		report.AliasSuppressionDB = measureAlias(out, cfg)
	} else if cfg.OutRate > cfg.InRate {
		report.ImageSuppressionDB = measureImage(out, cfg)
	}
	return report, nil
}

// bandPowerRatio 用 Blackman-Harris 加窗 STFT 计算信号在频带 [lo,hi]
// 内的功率与全频带功率之比（分贝）。Blackman-Harris 旁瓣约 -92dB，
// 窗泄漏不会成为测量底噪。
func bandPowerRatio(out []float64, rate int, lo, hi float64) float64 {
	const nfft = 4096
	const hop = nfft / 4
	win := make([]float64, nfft)
	for i := range win {
		win[i] = blackmanHarris(float64(i) / float64(nfft-1))
	}
	binHz := float64(rate) / nfft
	binLo := int(math.Ceil(lo/binHz)) + 1
	binHi := int(math.Floor(hi/binHz)) - 1
	if binHi < binLo || binLo >= nfft/2 {
		return math.NaN()
	}
	if binHi >= nfft/2 {
		binHi = nfft/2 - 1
	}

	re := make([]float64, nfft)
	im := make([]float64, nfft)
	var bandPower, totalPower float64
	for start := 0; start+nfft <= len(out); start += hop {
		for i := 0; i < nfft; i++ {
			re[i] = out[start+i] * win[i]
			im[i] = 0
		}
		fft(re, im)
		for bin := 1; bin < nfft/2; bin++ {
			power := re[bin]*re[bin] + im[bin]*im[bin]
			totalPower += power
			if bin >= binLo && bin <= binHi {
				bandPower += power
			}
		}
	}
	if totalPower == 0 {
		return math.NaN()
	}
	return 10 * math.Log10(bandPower/totalPower)
}

// measurePassband 用“未做相位量化”的精确分数相位加窗 sinc 重建参考
// 输出，比较通带 [0.04*低奈奎斯特, 0.4*低奈奎斯特) 内误差谱功率相对
// 参考信号谱功率的分贝比。
func measurePassband(out, in []float64, cfg Config, _ func(float64) float64) float64 {
	ref := referenceOutput(in, cfg)
	// 跳过启动暂态：前 2L 个输出有抽头落在零样本区，参考与流式输出
	// 均不完整，不纳入稳态通带误差统计。
	l := cfg.HalfLength
	if l == 0 {
		l = defaultHalfLength
	}
	startup := 2 * l
	n := len(out)
	if len(ref) < n {
		n = len(ref)
	}
	if n <= startup {
		return math.NaN()
	}
	// 参考与流式输出在相同时刻、相同相位约定下逐样本对齐，直接做
	// RMS 比较；多相相位量化误差约 1/(2P) 个输入样本，该口径可如实反映。
	var errSq, sigSq float64
	for i := startup; i < n; i++ {
		d := out[i] - ref[i]
		errSq += d * d
		sigSq += ref[i] * ref[i]
	}
	if sigSq == 0 {
		return math.NaN()
	}
	return 10 * math.Log10(errSq/sigSq)
}

// referenceOutput 用精确（未量化到 1/P）分数相位的加窗 sinc 对整段输入
// 一次处理，作为流式多相重采样器的理想对照；越界样本按零处理。
func referenceOutput(in []float64, cfg Config) []float64 {
	l := cfg.HalfLength
	if l == 0 {
		l = defaultHalfLength
	}
	fs := float64(cfg.InRate)
	fc := cfg.CutoffHz
	if fc == 0 {
		fc = cutoffGuard * 0.5 * float64(minInt(cfg.InRate, cfg.OutRate))
	}
	ratio := float64(cfg.InRate) / float64(cfg.OutRate)

	nOut := int(float64(len(in))*float64(cfg.OutRate)/float64(cfg.InRate)) + 2*l
	out := make([]float64, 0, nOut)
	nIn := int64(len(in))
	for m := 0; ; m++ {
		// 与 Resampler.Process 完全相同的位置/相位/边界约定：
		// 第 m 个输出对应输入位置 x=(m+1)*InRate/OutRate，最后抽头
		// 越界即停止，与流式 last>=nIn 的 break 条件逐一对齐。
		x := float64(m+1) * ratio
		k0 := int64(math.Floor(x))
		last := k0 + int64(l) - 1
		if last >= nIn {
			break
		}
		frac := x - math.Floor(x)
		acc, liveSum := 0.0, 0.0
		for j := 0; j < 2*l; j++ {
			t := j - l
			d := float64(t) - frac
			u := (float64(j) + 0.5) / float64(2*l)
			w := 2 * fc / fs * sinc(2*fc/fs*d) * window(cfg.Window, u)
			idx := k0 + int64(t)
			if idx >= 0 && idx < nIn {
				acc += w * in[idx]
				liveSum += w
			}
		}
		if liveSum == 0 {
			out = append(out, 0)
			continue
		}
		out = append(out, acc/liveSum)
	}
	return out
}

// measureAlias 针对下采样：扫频信号在输入奈奎斯特附近结束，混叠分量
// 折叠回 [0,outNyq)。选择远离滤波器过渡带的频带 [0.5*outNyq,0.8*outNyq)
// ——该区间内基带扫频早已结束，能量只可能来自混叠——统计其相对全频带
// 功率的分贝比。
func measureAlias(out []float64, cfg Config) float64 {
	outNyq := float64(cfg.OutRate) / 2
	// 默认截止频率在 0.85*outNyq，Blackman 窗过渡带约 0.1*outNyq，
	// 因此 [0.96*outNyq, 0.995*outNyq) 已位于阻带平坦区，该区间内
	// 检测到的能量只可能来自混叠折叠。
	return bandPowerRatio(out, cfg.OutRate, 0.96*outNyq, 0.995*outNyq)
}

// measureImage 针对上采样：频谱镜像以 OutRate 为中心，其落入输出
// 奈奎斯特内的边带为 [OutRate-InRate/2, OutRate/2)，统计该镜像
// 频带功率相对全频带功率的分贝比。
func measureImage(out []float64, cfg Config) float64 {
	inNyq := float64(cfg.InRate) / 2
	outNyq := float64(cfg.OutRate) / 2
	// 基带频谱只占据 [0, InRate/2)。上采样插值产生的镜像频谱以
	// OutRate 为中心周期延拓，其落入输出奈奎斯特内、且与基带不重叠
	// 的可测边带为 [InRate/2, OutRate/2)；该区间能量即镜像泄漏。
	lo := inNyq
	hi := outNyq
	if hi <= lo {
		return math.NaN()
	}
	return bandPowerRatio(out, cfg.OutRate, lo, hi)
}

func blackmanHarris(u float64) float64 {
	return 0.35875 - 0.48829*math.Cos(2*math.Pi*u) +
		0.14128*math.Cos(4*math.Pi*u) - 0.01168*math.Cos(6*math.Pi*u)
}

// fft 原地计算长度必须为 2 的幂的 radix-2 DIT 复数 FFT。
func fft(re, im []float64) {
	n := len(re)
	// 位反转置换。
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		ang := -2 * math.Pi / float64(length)
		wRe := math.Cos(ang)
		wIm := math.Sin(ang)
		for start := 0; start < n; start += length {
			curRe, curIm := 1.0, 0.0
			for k := 0; k < half; k++ {
				a := start + k
				b := a + half
				tRe := curRe*re[b] - curIm*im[b]
				tIm := curRe*im[b] + curIm*re[b]
				re[b] = re[a] - tRe
				im[b] = im[a] - tIm
				re[a] += tRe
				im[a] += tIm
				curRe, curIm = curRe*wRe-curIm*wIm, curRe*wIm+curIm*wRe
			}
		}
	}
}
