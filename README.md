# 线程局部身份泄漏

线程池复用线程时，上个请求的身份可能被下个请求继承。为Go插桩宿主写可导入的上下文监视包，输入线程切换、设置身份、借用、异步派生与退出事件；借用嵌套按栈恢复，异步派生只能携带显式快照，退出后栈必须归零。返回跨请求读取旧身份的来源链，用重入异常和线程复用序列验证。

## 包：`netsec`

`example.org/netsec/q0402` 下的纯 Go、无第三方依赖、并发安全的可导入包。

### 事件模型

| 事件 | 作用 | 违规时的发现项 |
| --- | --- | --- |
| `EventSwitch` | 请求在工作线程上开始运行；同线程不同请求即线程复用 | `StaleIdentityRead` |
| `EventSetIdentity` | 压入请求的基础身份帧；跨请求覆盖旧帧先报泄漏再重置 | `StaleIdentityRead` |
| `EventBorrow` / `EventReturn` | 嵌套借用，严格 LIFO 出栈 | `ReentrancyViolation` |
| `EventSpawn` | 异步派生；仅 `Snapshot` 可携带身份，`Inherit` 隐式继承被拒 | `ImplicitInheritance` |
| `EventExit` | 请求退出，无论何种残留都强制栈归零 | `DirtyExit` |

### 核心约定

- 每个线程维护一个身份帧栈，基础帧来自 `SetIdentity`，借用帧来自 `Borrow`。
- 借用必须按嵌套顺序归还；空栈归还、归还基础帧、标签不匹配都产生重入异常且不破坏栈。
- 异步线程只能通过 `Monitor.Snapshot` 产出的不可变快照令牌继承身份，快照在来源链上追加 `snapshot` 跳。
- 退出后 `StackDepth` 必为 0；存在未归还借用时先报 `DirtyExit` 再清零。
- 每个发现项带完整 `Provenance` 来源链（`set_identity -> borrow -> ... -> snapshot`），可用 `Finding.SourceChain()` 渲染。

### 最小用法

```go
mon := netsec.New()
mon.Handle(netsec.Event{Kind: netsec.EventSwitch, ThreadID: "w-1", RequestID: "r-old"})
mon.Handle(netsec.Event{Kind: netsec.EventSetIdentity, ThreadID: "w-1",
	Identity: netsec.Identity{RequestID: "r-old", Principal: "alice"}})

// 线程池把同一工作线程分给新请求
mon.Handle(netsec.Event{Kind: netsec.EventSwitch, ThreadID: "w-1", RequestID: "r-new"})
for _, f := range mon.Findings() {
	fmt.Println(f.Kind, f.SourceChain()) // stale_identity_read set_identity#w-1(alice)
}
```

### 验证

- `go test -race ./...` 覆盖线程复用序列、来源链、全部重入异常、快照/隐式派生、退出归零与并发摄入。
