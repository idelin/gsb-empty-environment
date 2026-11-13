# 纪元回收漏掉休眠读取者

并发策略表用纪元回收旧规则。读取线程若在休眠前没有正确公布状态，醒来后可能解引用已回收的代次。本包
`example.org/netsec/q0069` 提供可导入的具体状态模型、未知交错下的最短见证搜索，以及对待审实现推进/回收
决定的复核。

## 模型

- 全局纪元为 `E0..E3` 循环；最多 3 个线程。
- 线程事件：`Enter`、`LoadRoot`、`Dereference`、`Exit`、`Quiescent`。
- 更新者事件：`ReplaceRoot`、`Retire`、`TryAdvance`。
- `Enter` 记录当前纪元、置 active；`Dereference` 只允许 active 线程执行。
- `Exit` 只置 inactive，不清除线程局部保存的 ID/代次，因此退出后重新进入可暴露旧代次。
- `Quiescent` 只允许 inactive 线程执行，不能代替 `Exit`；不会改变 active 状态。
- `ReplaceRoot` 复用同一地址时创建新代次；旧代次未回收前拒绝地址复用。
- `Retire` 记录退休纪元和当时的成功推进计数。
- `TryAdvance` 仅当所有 active 线程的已公布纪元都等于当前纪元时推进；它不会替休眠线程更新已公布纪元。
- 每个退休代次只有在“两个安全推进”且回收时没有 active 线程持有该 ID/代次时才能回收。安全推进按每对象
  计数，不用 `(当前纪元-退休纪元)%4` 判断，避免回绕后误判。
- 默认模型只施加规格明确写出的 `Dereference` 的 active 前置条件。若需更严格的读侧纪律，可设置
  `Options.RequireActiveForLoadRoot`。
- `Options.TreatQuiescentAsExit` 仅用于诊断“把 active 线程的 Quiescent 当成 Exit”的待审实现；正确模型默认
  拒绝该事件，复核会在首个此类事件处报告不安全决定。

## 最短见证

```go
p := netsec.NewProgram(
	[]netsec.Event{
		netsec.Enter(), netsec.LoadRoot(), netsec.Exit(),
		netsec.Enter(), netsec.Dereference(),
	},
	[]netsec.Event{
		netsec.ReplaceRoot(1), netsec.RetireObject(1),
		netsec.TryAdvance(), netsec.TryAdvance(),
	},
)

w, err := netsec.FindShortestWitness(p)
// w.Length() == 9
// w.Actors() == []int{0,1,0,0,1,1,1,0,0}
// w.Violation.Ref == ObjectRef{ID: 1, Gen: 0}
```

搜索对每个线程脚本（至多 8 个事件）枚举合法交错；返回最短的“回收后解引用旧代次”见证，等长时按事件所属线
程号序列的字典序最小者返回。不存在见证时返回 `(nil, nil)`。

## 复核待审决定

```go
steps := []netsec.ProposedStep{
	{Thread: 0, Event: netsec.Enter()},
	{Thread: 1, Event: netsec.ReplaceRoot(1)},
	{Thread: 0, Event: netsec.LoadRoot()},
	{Thread: 0, Event: netsec.Quiescent()}, // active 线程不得 Quiescent
}

result, err := netsec.ReviewProposed(steps)
// result.FirstUnsafe.Step == 3
// result.FirstUnsafe.Kind == netsec.DecisionEvent
```

`ProposedStep.ClaimedAdvance` 记录待审实现声称的 `TryAdvance` 结果，
`ClaimedReclaim` 记录它声称回收的代次。复核按正确模型重放，返回首个不安全决定：被正确模型拒绝的事件、
实际不能成功却声称成功的推进、或尚不满足两个安全推进/仍有 active 持有者的回收。更保守的决定只记为
`Mismatches`，不算不安全。

## 测试

```sh
go test ./...
```

测试覆盖休眠 active 线程、连续与失败推进、纪元回绕、地址复用、正常退出、`Quiescent` 误用、最短见证与
线程号 tie-break，并对 6240 个小程序同时运行 BFS 与无剪枝暴力枚举作差分；回收计数按退休纪元和推进次数
的全部组合穷举。
