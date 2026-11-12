# 自动剪辑要对齐两路录音，其中一路时钟缓慢漂移

自动剪辑要对齐两路录音，其中一路时钟缓慢漂移。写供Go音频分析宿主调用的对齐包，在重叠窗口计算归一化互相关得到候选延迟，静音窗不参与；每窗代价为1减相关系数，另加输入权重乘相邻延迟差绝对值，用动态规划选择整段延迟曲线，限制每窗漂移速度。输出窗口中心的有理样本偏移和置信差，不直接修改音频。用人工插入已知漂移、间歇静音与重复节拍的PCM，比较小候选空间全路径枚举，歧义不足时明确返回多个最优路径。

## 实现：`audioalign` 子包

包路径 `example.org/domainreissue/q057/audioalign`，入口为
`audioalign.Align(ref, delayed []float64, cfg audioalign.Config) (*audioalign.Result, error)`。
输入切片只读，包内不写回任何样本。

### 处理流程

1. 以 `WindowSize`/`HopSize` 取重叠窗，对每窗在 `[MinLag, MaxLag]`
   整数样本范围内计算去均值归一化互相关（Pearson NCC）。
2. 任一轨窗内 RMS 低于 `SilenceRMS`、零方差、或在该 lag 下无重叠样本，
   该候选/窗口跳过；整段无可用窗返回 `ErrNoActiveWindows`。
3. 每窗局部代价 `cost = 1 - corr`。动态规划在相邻活动窗之间转移，
   转移附加 `SmoothWeight * |lag_i - lag_{i-1}|`，并要求
   `|lag_i - lag_{i-1}| <= MaxDrift * hopDist`（跨静音窗按 hop 数缩放）。
4. 代价在 `CostEpsilon` 内视为等优，等优转移与等优终态全部保留，
   回溯出全部全局最优路径；数量超过 `MaxPaths` 时置 `Result.Truncated`。
5. 输出每个活动窗：中心样本位置、全部候选、最佳 NCC、
   置信差（最佳与次佳 lag 的相关系数之差，缺失次佳按 -1 计）、
   以及歧义标记。偏移用 `Rational{Num, Den}` 表示；关闭子样本细化时
   `Den==1`，开启抛物线细化后量化到 `SubsampleQ` 并约分。

### 测试

`go test ./...`（含 `-race`）覆盖：

- 合成 PCM：线性时钟漂移 + 间歇静音（双轨/单轨）+ 重复节拍，
  验证恢复的延迟曲线与已知漂移一致、漂移限速被遵守、静音窗被跳过。
- `TestBruteForceEquivalence`：把每窗限制为 3 个候选的小空间，
  独立全路径枚举与 DP 的最优路径集合、代价逐项一致。
- `TestAmbiguousRepeatedBeats`：周期性重复节拍产生两个完全等优 lag，
  明确返回多条等代价最优路径；受控歧义空间验证 `MaxPaths` 截断。
- 子样本有理偏移、全静音/越界/非法配置错误、输入不被修改。

覆盖率约 90%。
