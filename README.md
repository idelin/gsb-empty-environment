# STFT 分析与重建

`stft` 包为 Go 音频管线提供流式 STFT 分析与加权重叠相加重建。窗口和 hop
完全由调用方提供；每帧起点为 `m*hop - len(window)/2`，输入范围外按显式零填充。
分析帧乘一次窗口，逆变换后再乘一次窗口并重叠相加，每个有效样本除以该点实际
累加的 `Σw²`，因此输出不依赖输入分块大小，也不会在块边界产生周期性音量波动。

```go
window := make([]float64, 256)
for i := range window {
	window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(window)))
}

a, err := stft.New(window, 64, func(spectrum []complex128) error {
	// 可原地修改频谱；修改后必须保持共轭对称。
	return nil
})
if err != nil {
	return err
}

var pcm []float64
for _, chunk := range chunks {
	out, err := a.Push(chunk)
	if err != nil {
		return err
	}
	pcm = append(pcm, out...)
}
tail, err := a.Flush()
if err != nil {
	return err
}
pcm = append(pcm, tail...)
// len(pcm) == 输入总样本数，整除关系不必满足 hop。
```

整段便捷入口为 `stft.ProcessAll(window, hop, callback, samples)`；模块根包也导出
同名别名。恒等回调（`nil` 或返回 `nil` 的 `SpectrumFunc`）可精确重建。

## 行为约定

- 回调收到长度为 `len(window)` 的完整复数频谱；对实信号，`X[k]` 与 `X[N-k]`
  必须共轭对称，DC 与 Nyquist 必须为实数，否则返回 `ErrNonHermitian`。
- 有效样本的窗口平方和恰为零时返回 `ErrZeroWindowSum`，不会产生 `Inf`/`NaN`。
- `Flush` 补齐末尾零填充并返回剩余样本；所有 `Push` 与 `Flush` 输出拼接后
  正好等于输入样本数。`Flush` 幂等，之后再次 `Push` 返回 `ErrFlushed`。
- `Reset` 清空流状态，保留窗口、hop 与回调，可重新处理下一段音频。

## 验证

`go test ./...` 覆盖单位脉冲（含首尾样本）、正弦、随机 PCM、任意分块与整段
一致性、恒等回调误差、flush 精确样本数、零窗口能量拒绝和共轭对称拒绝。
