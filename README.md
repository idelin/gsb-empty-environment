# 恶意归档的流式展开预算

`example.org/netsec/q-019` 提供一个可导入的 Go 包，用于在不可信嵌套容器树上进行
全量预算约束的流式展开。解码回调只能写预算包提供的 `io.Writer`，结果只有在整棵树
全部成功后才发布。

## 快速使用

```go
root := netsec.Container("archive",
	netsec.Stream("readme.txt", 5, func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "hello")
		return err
	}),
)

res, err := netsec.Expand(ctx, []*netsec.Node{root}, netsec.Budget{
	MaxDecodedBytes: 1 << 20,
	MaxDepth:        8,
	MaxLeaves:       1000,
	MaxLiveBuffer:   4 << 20,
})
if err != nil {
	// res == nil；所有暂存已被 Reset。
	return err
}
use(res.Roots)
```

## 预算口径

- `MaxDecodedBytes` 只统计受控 Writer 实际接受的字节。写入会在落盘前原子检查，
  超限写入返回 `0` 字节的 `*BudgetError`。
- `MaxDepth` 按根到叶路径上的节点数计算，根节点深度为 1。
- `MaxLeaves` 统计终止节点引用，包括空子容器；同一个 `*Node` 被引用多次时每次
  都独立计数并独立解码。
- `MaxLiveBuffer` 统计同时存活的暂存字节数，峰值记录在 `Stats.PeakLiveBytes`。
- `Node.DeclaredSize` 仅作提示，从不用于预分配、预算判断或校验。

## 失败与并发

校验阶段先用迭代 DFS 检查节点形态和环；环会在任何解码回调运行前以 `ErrCycle`
拒绝。规划阶段落实深度和叶数量预算，之后最多 `WithConcurrency(n)` 个流并行解码。
任一预算超限、短写、回调错误、panic 或取消都会取消共享上下文，所有后代停止写入。
失败时返回 `nil` 结果并回收全部暂存；不会暴露部分树。

可用 `WithStageFactory` 注入短写或计量型暂存，用 `WithConcurrency` 控制并行度。
错误可用 `errors.Is/As` 判定：`ErrBudgetExceeded`、`ErrDepthExceeded`、
`ErrLeavesExceeded`、`ErrLiveExceeded`、`ErrCycle`、`io.ErrShortWrite`。
解码回调必须响应 `ctx` 或 Writer 错误；Go 无法强制终止完全无视取消信号的回调。

## 测试

```sh
go test ./...
go test -race -count=20 ./...
```

测试覆盖：谎报长度、共享引用重复计费、环、并发子流、预算触发取消、外部取消、
注入短写、解码 panic、回调返回后的迟到写入，以及通过注入暂存实测的峰值缓冲上限。
