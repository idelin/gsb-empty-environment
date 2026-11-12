# 多声道音频相位关联诊断

多声道音频下混时，一条通道极性反转会让人声消失。本包给 Go 音频质检宿主提供纯诊断能力，不修改音频，也不会自动反相。

## 判定流程

- 固定窗内先减去每通道均值，再计算 Pearson 归一化相关 `corr(A,B)` 与能量比 `energy(A)/energy(B)`。
- 任一通道 RMS 不大于 `SilenceRMS` 时，该对通道标记为 `silent`（未知）。
- 能量比不在 `[MinEnergyRatio, MaxEnergyRatio]` 时标记为 `energy_ratio_out_of_range`（未知）。
- `|corr|` 未达到 `CorrelationThreshold` 时标记为 `weak_correlation`（未知）。
- 只有可用证据才建边。相关为正建 `+1` 边，意为同极性；相关为负建 `-1` 边，意为反极性。
- 同一通道对的同号证据必须连续出现 `ConfirmationWindows` 个窗口，才进入确认图。
- 对确认图做带符号二分性检查。一致时给出每个通道的 `+1/-1` 极性赋值；矛盾时给出边数最短的负符号环。

相关是零延迟相关。轻度延时通常仍得到正相关；延时增大后相关会下降并可能因阈值变成未知，本包不估计时延。

## 使用方式

```go
import taskpkg "example.org/domainreissue/q065"

cfg := taskpkg.DefaultConfig()
cfg.WindowSize = 1024
cfg.CorrelationThreshold = 0.90
cfg.MinEnergyRatio = 0.5
cfg.MaxEnergyRatio = 2.0
cfg.ConfirmationWindows = 2

report, err := taskpkg.Analyze(
    []string{"L", "R", "C"},
    [][]float64{left, right, center},
    cfg,
)
```

`report` 包含原始窗证据、通道对连续命中摘要、确认边、一致性结果、极性赋值及最短矛盾环。`PairSummary.Confirmed` 表示历史上曾达到连续命中要求，`Active` 表示直到最后一个完整窗仍处于连续确认状态；`Sign` 对应确认图使用的证据方向，`CurrentSign` 对应当前连续段。矛盾环的 `Nodes` 是通道下标，`Steps` 中每一步的 `EdgeIndex` 指回 `report.Edges`，可直接用于质检告警定位。

流式宿主可使用 `NewAnalyzer`、`Push` 和 `Finish`。完整固定窗只取决于样本位置；任意分块（含重叠窗配置）与一次性分析得到相同报告，尾部不足一窗的数据会被忽略。

## 验证

```sh
go test ./...
```

测试覆盖同相、反相、轻度延时、大延时、静音、能量比越界、冲突三角、最短矛盾环、跨窗确认，以及任意分块/一次性分析等价。
