# discretecomp — 离散隐私损失分布的精确组合

供 Go 隐私宿主调用的纯标准库包（`math/big` 有理运算，无浮点误差），用于核验统计服务连续发布多个**独立**机制结果时的累计泄漏。

## 建模

每个机制给出相邻数据集下的两份离散有理分布 `P`、`Q`（同支撑、非负、各自归一化到 1）：

- `NewMechanism(p, q)` 按似然比 `P(x)/Q(x)` 把输出合并成桶；比值相同的输出质量累加。
- 零概率事件不进有限桶，单列到 `Mechanism.Zero`：
  - `POnly`：`P(x)>0, Q(x)=0`，比值 `+Inf`；
  - `QOnly`：`P(x)=0, Q(x)>0`，比值 `0`；
  - `BothZero`：两侧均为零。

## 组合与松弛

- `Compose(mechs)` 对独立机制构造乘积分布，按似然比乘积合并桶（全枚举，适合小机制）。
- `Profile.Delta(budget, dir)` 返回给定**有理乘性界** `e^ε = budget ≥ 1` 的精确松弛，即 hockey-stick 散度
  - `DeltaPOverQ`：`δ = POnly + Σ_{ratio>budget}(p − budget·q)`；
  - `DeltaQOverP`：`δ = QOnly + Σ_{ratio<1/budget}(q − p/budget)`。
  这就是该方向满足 `(ε, δ)`-DP 的最小 `δ`。

## 预算截断

乘积空间过大时，`ComposeTruncated` 用按乘积似然比排序的最大堆做最佳优先枚举：

- `TruncateOptions.MassBudget`：允许留在未枚举尾部的有限质量占比（`[0,1]`，`0` 即全枚举）；
- `TruncateOptions.MaxNodes`：弹出桶数的硬上限；
- 零概率事件始终精确计入，不参与截断；
- 尾部对 `δ` 的贡献不超过其自身分子质量，故返回严格的 `[Lower, Upper]`，区间宽度恰为剩余质量。

## 验证

`go test ./...` 覆盖：等比并桶、输入校验、零概率事件、纯 DP 阈值、质量归一化、手工算例，以及随机小机制下截断上下界对全枚举精确值的夹逼（含节点上限、零预算精确、满预算空枚举等边界）。
