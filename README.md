# 多路径检查结果拒绝优先

安全网关并行运行多个检测器，快速允许不能覆盖稍后返回的关键拒绝。写供Go网关调用的判定组合包，策略是带阈值的允许组与任一命中即拒绝组，各检测器给已证明结果或未知；只有所有仍可能改变结论的分支都已结束才能定案。计算当前结论是否可提前确定并取消无关工作，返回最小决定证据。最多二十检测器，用全部剩余结果补全核对提前结束不会改变最终判定。

## 判定语义

- 检测器分为两组：允许组（`AllowGroup`，累计已证明允许，达到 `AllowThreshold` 才
  可能放行）与拒绝组（`DenyGroup`，任一已证明拒绝即拒绝）。
- 拒绝优先：拒绝组一旦命中即锁定 `Deny`（原因 `DenyHit`），已凑齐的快速允许不能
  覆盖，其余未结束检测器全部列入 `Irrelevant` 供网关取消。
- 允许组剩余可能票数也凑不齐阈值时，fail-closed 锁定 `Deny`（原因
  `AllowUnreachable`），无需再等拒绝组。
- 否则结论为 `Pending`，`Relevant` 给出仍可能翻转结论、必须等待的分支。
- `Status.Evidence` 是支撑结论的最小决定证据：去掉其中任意一个检测器，结论即
  不再被当前证据锁定。

## 提前结束的穷举核对

`VerifyInvariant` / `VerifyEarlyStop` 把每个未结束检测器按“证明本组命中与否”
补全（上限 20 个检测器，补全空间至多 2^20），逐组计算最终判定，确认：

- 已声称定案时，所有剩余结果补全的最终判定都与当前结论一致；
- `Relevant` 与 `Irrelevant` 恰好划分全部未结束检测器；
- 证据充分且包含意义上最小（`VerifyEvidence`）。

## 用法

```go
c, _ := netsec.New(netsec.Config{
    AllowGroup:     []int{0, 1, 2},
    DenyGroup:      []int{3, 4},
    AllowThreshold: 2,
})

// 并行检测器完成后提交已证明结果；无法判定提交 netsec.Unknown。
_ = c.Submit(0, netsec.ProvenAllow)

st := c.Evaluate()
switch st.Verdict {
case netsec.Pending:
    // 等待 st.Relevant 中的检测器，st.Irrelevant 可立即取消
case netsec.Allow, netsec.Deny:
    // st.Evidence 即最小决定证据，st.Irrelevant 已被组合器标记取消
}

// 测试/审计：穷举全部剩余补全，核对提前结束不改变最终判定。
_, err := netsec.VerifyEarlyStop(cfg, c.Snapshot())
```

完整并行调度与取消示例见 `example_test.go`。
