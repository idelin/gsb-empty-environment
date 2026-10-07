# batchclient

Go 分批请求客户端：把请求切片分批发送，响应按请求 id 对齐，最终结果严格按输入顺序返回。

## 核心语义

- **按 id 对齐**：响应可乱序、部分缺失或重复，每项以请求 id 对齐；未知 id 的响应被忽略。
- **批级失败隔离**：批次级错误不覆盖该批已确认的项；仅缺失项进入重试，且保持原请求 id。
- **版本裁决**：同项可跨多个尝试在途（尝试超时后旧调用不取消）。终态以服务端单调提交版本裁决——高版本胜出，旧失败不能覆盖新成功；同版本异结果将该项隔离为冲突（`StatusConflict`），不影响其他项。
- **取消语义**：`context` 取消仅停止未派发项；已在途的批次继续执行至完成，结果照常入账。
- **检查点恢复**：`Run` 返回的 `Checkpoint` 保留请求身份、确认版本与缺失集；`Resume` 从检查点续跑，仅重发缺失项，已确认状态不被回放覆盖。

## 用法

```go
tr := batchclient.NewScriptTransport() // 或任意实现 Transport 的传输层
c := batchclient.New(tr,
    batchclient.WithBatchSize(64),
    batchclient.WithMaxAttempts(3),
    batchclient.WithParallelism(8),
    batchclient.WithAttemptTimeout(500*time.Millisecond),
)
res := c.Run(ctx, reqs)
if !res.Complete() {
    // 稍后从检查点恢复（可跨进程序列化 Checkpoint）
    res = c.Resume(ctx, res.Checkpoint)
}
for _, o := range res.Outcomes { /* 与输入顺序一一对应 */ }
```

## 可编排传输层

`ScriptTransport` 按调用顺序消费脚本步骤（`Respond` / `RespondErr` / `FailBatch` / `CommitExcept` / `Gate`），脚本耗尽后默认提交全部请求；`Commit`/`CommitFail` 为每项分配单调提交版本。测试用它编排乱序、缺失、重复、冲突、慢在途与故障重放场景。

## 测试

```sh
go test -race ./...
```
