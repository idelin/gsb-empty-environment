# 协议消息因果顺序修复

抓包包含观测误差，安全测试需要找离原序列改动最少的合法握手解释。写供Go分析宿主调用的求解包，消息保留发送先于接收、每端本地顺序及事务关联，最多十八消息；求相邻交换次数最少的合法排列，并列按消息ID。无解返回冲突边环，用全排列的小实例核对，不能删除失败消息获得合法结果。

## 包：`handshake`

导入路径 `example.org/netsec/q0429/handshake`。

- `Solve(messages)`：按消息字段自动构造三类因果边（发送先于接收、同端点本地顺序、同事务顺序），返回最小相邻交换数的合法排列。
- `SolveWithEdges(messages, edges)`：通用入口，宿主可自行提供 `Edge` 约束。
- 结果 `Result{Order, Swaps}`：`Order` 包含全部输入消息 ID（不删除任何消息），`Swaps` 等于相对观测序列的逆序对数，即最少相邻交换次数；并列时取消息 ID 字典序最小者。
- 无解时返回 `*CycleError`，其中 `Ring` 为闭合冲突环（首 ID 在末尾重复），`Edges` 给出环上每条边及其 `Reason`。
- 消息数超过 `MaxMessages`（18）或字段非法时返回错误。

实现使用 O(n²·2ⁿ) 位掩码动态规划（n ≤ 18），环检测为迭代式三色 DFS。`handshake_test.go` 用全排列暴力枚举在小实例上逐例核对最优值、并列规则与无解判定。

```go
result, err := handshake.Solve(messages)
var cycleErr *handshake.CycleError
if errors.As(err, &cycleErr) {
    // 使用 cycleErr.Ring / cycleErr.Edges 报告冲突
}
```
