# 云事件身份序列关联

云审计里删除凭据与使用凭据可能乱序上报，不能直接按到达顺序判定违规。给Go检测宿主实现时间区间关联包，事件给可信提交序号与可能发生时间区间，先依据序号约束收窄区间，再判断使用必然早于、必然晚于或可能跨撤销边界。保留冲突事件而非任取一份，用小时间域枚举核对结论。

## 模型

- `Event` 携带事件类型（使用/撤销）、凭据 ID、提交通道 `Channel`、通道内可信序号 `Sequence` 以及可能发生的离散时间闭区间 `[Lo, Hi]`。
- 同一通道内序号是**严格全序**：序号小的事件发生 tick 必然更小；不同通道之间没有序号关系，只能靠时间区间约束。
- 到达顺序完全不可信，包内按 `(Channel, Sequence)` 重新排序处理。

## 关联流程

1. **序号收窄**（`Correlator.Narrow`）：同通道事件按序号排序，前向传播严格下界、反向传播严格上界，得到每个事件在可行赋值下能取到的最紧区间；再求出每个事件单独可达的最小/最大 tick（`MinTick/MaxTick`）。
2. **边界判定**（`Correlator.Correlate`）：对同凭据的每个“使用 × 撤销”配对给出
   - `VerdictBefore`：使用必然早于撤销（凭据仍有效）；
   - `VerdictAfter`：使用必然晚于撤销（确定违规）；
   - `VerdictStraddle`：两种次序都可达，或所在通道本身无解（冲突保留，附 `Conflict` 说明，不丢事件）。
3. **枚举核对**（`EnumerateOrderings` / `CrossCheck`）：在小时间域上枚举各通道全部严格递增赋值，对每个可行现实统计使用与撤销的前/后/同 tick 关系；解析结论与枚举结论不一致时产出 `Mismatch`，受预算限制时通过 `Exhaustive`/`TruncatedChannels` 显式标注。

## 用法

```go
c, err := netsec.NewCorrelator(events)
if err != nil { /* 输入校验失败 */ }

report, cross := c.CrossCheck(netsec.DefaultEnumBudget)
for _, finding := range report.Findings {
    switch finding.Verdict {
    case netsec.VerdictAfter:
        // 确定违规：使用必然发生在撤销之后
    case netsec.VerdictStraddle:
        // 证据冲突或边界可能被跨越，finding.Conflict 给出原因
    }
}
if len(cross.Mismatches) != 0 {
    // 解析实现与枚举预言机不一致（正常应为空）
}
```

`go test ./...` 包含固定场景、不可行通道冲突保留，以及 300 组随机小时间域场景下解析结论与穷举枚举的对拍。
