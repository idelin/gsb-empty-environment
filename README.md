# causalslice — 因果一致切片选取（单进程事件模拟器）

在带**因果标签**（显式依赖边 + 向量时钟）的多对象、多副本 KV 存储上，为一次只读请求选取
**因果一致切片**：读到某个后果版本时，它的全部原因版本都必须在同一切片内；原因不可得时
切片保持打开、把缺失版本作为**阻塞依赖**上报，绝不返回“有后果无原因”的切片。

通信（延迟/乱序/丢包/分区）、故障（崩溃/恢复）、时间（逻辑时钟）全部在一个**单进程事件
模拟器**内受控，没有真实网络或线程，执行完全确定（固定种子可逐位复现）。

## 构建与运行

```sh
go test -race ./...        # 全部测试
go run ./cmd/demo          # 运行样例
```

## 模型

- 事件堆按 `(逻辑时间, 序号)` 释放；`Sim.Send` 先经 `Network` 策略决定延迟/丢弃，再入堆；
  发往崩溃副本的在途消息按丢包处理。
- 版本 `Version{ID, Key, Val, Deps []VersionID, VC map[string]int}`：
  - `ID = (Origin, Seq)` 唯一标识一次写；
  - `Deps` 是精确的因果过去（B 依赖 A 就显式列出 A）；
  - `VC` 是该版本的向量时钟，用于并发版本的确定性择优。
- 副本存储收到的每个版本（即使依赖尚未到达），只在向量时钟序下推进 `latest[key]`；
  周期反熵（gossip）修复缺失版本。
- 写在目标副本缺因时被拒绝（`ErrCauseMissing`），**不产生后果版本**。

## 切片算法（每请求独立）

每个 `Client.Read` 新建一个 `Snapshot`，状态只存在于该客户端、只在事件处理器内修改；
副本上没有任何按请求存放的可变状态，不同请求的 map 互不相干（`Peek` 返回防御性拷贝）。

1. 对每个请求 key 向起始副本发 `FetchReq{Key, Dep=零}`。
2. 收到 `FetchResp{Ver, Closure, Missing}`：
   - 把 `Ver` 与本地已知祖先 `Closure` 全部 pin 进本快照；
   - 对每个缺失前沿 `Missing`，标记**等待**（“缺版本标等待”）。
3. 对等待的精确版本向尚未咨询过的存活副本发 `FetchReq{Dep=具体版本}`；定时器超时则轮转
   副本重传。精确索取得到 `Ver=nil` 时该依赖**仍未解决**，继续等待，绝不销账。
4. 关闭条件（全部成立才关闭）：每个初始 key 与每个已发出的精确依赖都已结算、在途为空、
   等待集为空。
5. 返回 `Result{Closed, Selected map[key]VersionID, Vector}`；`Peek` 随时返回当前选择与
   `BlockedDep{Missing, RequiredBy, Tried}`（**阻塞依赖**）。

## 安全性证据（Safety：永远不返回非因果切片）

定义切片的已固定集合 `P`。不变量：**每个被固定的版本 v，其 `Deps(v)` 中的每个 d，
要么已经在 `P` 中，要么在等待集 `W` 或一个在途的精确依赖请求中。**

- 固定版本时同时固定应答给出的完整已知祖先闭包（`closureFrom`），保证“固定 v”这一动作
  不会单独把后果放进 `P`。
- 唯一能从 `W` 移除一个依赖 d 的途径是 d 被固定；唯一能结算一个精确依赖请求的途径是
  `Ver==d != nil`（`Ver=nil` 重新入等待）。因此 `W` 与在途依赖只能因“原因到达”而消失。
- 关闭要求 `W` 为空且无在途依赖，故关闭瞬间对每个固定 v 都有 `Deps(v) ⊆ P`。
- `Selected` 只取请求 key 在 `P` 中的向量时钟最大者；因此“新 B ⇒ 对应 A 同切片”，且不会
  选入因果未来的版本。B 的精确缺失使切片**阻塞**而非返回旧/无因状态。
- 拒绝写从源头阻止“无因之后果”进入系统。

`TestSafetyNewBRequiresA` 在 r3 只有 B 没有 A 时断言：分区中切片不关闭、阻塞上报含
`r1#1`；愈合后同一快照才关闭且 `Selected` 同时含 `A=r1#1, B=r2#1`。

## 活性证据（Liveness：有条件最终闭合，并明确其失效条件）

在以下条件下切片必然关闭：副本集合有限；每条被重传的消息最终送达（有界丢包/延迟，或
超时轮转覆盖可达副本）；存在某个可达副本持有依赖闭包（反熵使版本可达）。

反证：未关闭则至少有一个初始 key 或精确依赖未结算。客户端对每个未满足项周期性重传并轮转
全部存活副本；有限集合 + 最终送达 ⇒ 某次请求到达持有者 ⇒ 返回版本及其闭包 ⇒ 该项被固定。
每固定一个版本，缺失前沿向因果过去移动；依赖图有限无环，前沿严格变短，迭代终止于空集 ⇒
关闭。`Vector` 随固定单调增长，不存在“选了又回退”。

当条件不成立——例如持有某原因的所有副本永久不可达，或所有副本永久崩溃——活性**无法
保证**：`TestPermanentUnavailabilityBlocksForever` 证明切片永不关闭，持续通过 `Peek`
上报阻塞依赖。即：安全性是无条件的，活性显式以系统模型假设为前提。崩溃/恢复路径由
`TestCrashRecovery` 覆盖（恢复后 gossip + 重传使切片闭合）。

## 测试

| 测试 | 证据 |
| --- | --- |
| `TestSafetyNewBRequiresA` | 有 B 无 A 时阻塞且上报原因；愈合后切片含成对 A/B |
| `TestPutRefusedWithoutCause` | 缺因的写被拒绝，不创建后果版本 |
| `TestLivenessBoundedDelays` | 有界延迟无故障下切片最终闭合 |
| `TestCrashRecovery` | 服务副本崩溃后恢复，切片仍闭合且因果封闭 |
| `TestPermanentUnavailabilityBlocksForever` | 原因永久不可达时永不闭合、持续上报阻塞依赖 |
| `TestSnapshotIsolation` | 并发请求不共享可变状态，`Peek` 为防御性拷贝 |
| `TestDeterministic` | 相同输入产生逐行一致的执行 |

## 运行样例（摘录）

```
t= 24  r3 stores r2#1 (B=2)               # r3 收到新 B
t= 25  reader opens snapshot #1 at r3 for [A B]
t= 27  snapshot #1 marks r1#1 waiting (needed for B; tried [r3])
t= 27  snapshot #1 fetches cause r1#1 from r1
t= 35  snapshot #1 timeout on r1#1 via r1, retry via r2
>>> mid-flight peek: closed=false selected=map[B:r2#1] blocked=[r1#1(needed-by:B tried:[r1])]
>>> partition healed
t= 48  snapshot #1 fetches cause r1#1 from r1
t= 51  snapshot #1 closes: selected={A=r1#1, B=r2#1} vector={r1:1, r2:1}

=== consistent slice ===
closed : true
versions selected: map[A:r1#1 B:r2#1]
version vector  : map[r1:1 r2:1]
```

切片在 A（`r1#1`）不可达期间保持打开并把它作为阻塞依赖上报；分区愈合后才关闭，返回所选
版本向量 `map[A:r1#1, B:r2#1]` 与合并版本向量 `{r1:1, r2:1}`。
