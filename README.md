# Go 多通道前视压缩器

为混音宿主实现的有状态动态范围组件。检测器取所有通道绝对值的最大值并联
动控制全部通道，音频经过固定长度的环形前视缓冲，参数更新不会清空缓冲、
不会丢样本，也不会改变延迟。

## 基本用法

```go
cfg := taskpkg.Config{
	SampleRate:       48000,
	Channels:         2,
	LookaheadSamples: 64,
	ThresholdDB:      -20,
	Ratio:            4,
	KneeDB:           6,
	AttackSeconds:    0.010,
	ReleaseSeconds:   0.100,
}

c, err := taskpkg.NewCompressor(cfg)
if err != nil {
	log.Fatal(err)
}

output, gainDB, err := c.Process(input)
```

`input` 和 `output` 使用 `[通道][样本]` 布局，也可用
`ProcessInterleaved` 处理交错样本。`gainDB` 是逐样本、按输出时间对齐的
增益轨迹，单位为 dB。`ProcessSample` 用于逐帧处理，
`ProcessWithParams` 用于在块边界更新参数。

## 处理语义

- 静态曲线使用题内软拐点公式；`KneeDB` 为 0 时退化为硬拐点。
- 包络在 dB 域按攻击/释放时间常数做一阶指数平滑：
  `a = exp(-1/(tau*fs))`，`env = a*env + (1-a)*target`。
- 音频延迟固定为 `LookaheadSamples`；`output[L]` 对应 `input[0]`。
- 当前检测样本决定当前输出增益，并作用于延迟后的音频。
- `SetParams` 只替换参数和滤波系数，保留延迟线、写指针与包络状态；
  新参数从下一次检测样本开始生效。
- 数字静音使用 `SilenceFloorDB`（默认 -200 dB）计算，增益为 0 dB；
  `LevelDB` 不会产生 `-Inf`，构造器拒绝 `Ratio <= 0`，避免瞬时除零。
- 输入中的 `NaN`/`Inf` 按 0 处理，输出与增益轨迹保持有限值。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖阶跃延迟、短脉冲完整性、多通道联动一致性、分块参数更新、
随意块切分、攻击/释放时间常数、交错布局以及静音与非有限值处理。
