# 弱引用回调复活已撤销能力：生命周期模型

本模块实现一个供 Go 运行时安全测试使用的宿主调用对象模型，并附带一个故意保留漏洞的对照实现和至多 3 线程的穷举事件搜索。

| 包 | 用途 |
| --- | --- |
| `example.org/netsec/q0070` | 正确实现、资源调用票据、全局槽和可并发运行的终结器 |
| `example.org/netsec/q0070/pending` | **故意脆弱，仅供审计/回归测试，禁止用于生产** |
| `example.org/netsec/q0070/eventsearch` | 3 线程交错 BFS 与最短反例 |
| `example.org/netsec/q0070/cmd/eventsearch` | 打印安全模型与待审模型的结果 |

## 安全模型

- 每个对象有唯一且永不复用的 `ObjectID`，以及可复用的 `Address` 和单调递增的 `AllocationGeneration`。地址复用后，旧弱引用、旧强令牌和旧方法票据全部失效。
- 强引用由一次性令牌表示。`StrongAdd` / `WeakGet` 只在 `live` 且强引用计数大于 0 时原子加一；`StrongDrop` 消费令牌，计数降到 0 时进入 `finalizing` 并只排队一次终结回调。
- `Ticket` 绑定对象 ID、地址、分配代次、能力代次和资源 ID。`Call` 在运行时锁内重新校验全部字段，并额外检查 `live`、强引用、撤销位和资源存活状态。只有校验全部成功才增加真实宿主调用计数。
- `Revoke` 先递增 `CapabilityGeneration`，再清除宿主资源。旧票据因此天然失效，即使终结器之后把任意资源 ID 放回对象也一样。
- `FinalizeContext` 只暴露 `WeakGet` 和 `RequestResurrect`。回调在无锁状态下执行；`Revoke` 可以与它并发。
- 回调结束后，仅当宿主显式允许、能力未被撤销且资源仍存活时，才接受第一个复活请求。其余请求仍被记录为 `duplicate_request`。被拒绝时对象转 `dead` 并清除资源。
- 已执行过终结器的对象再次失去最后一个强所有者时直接转 `dead`，不会排队第二次终结。
- 全局槽拥有自己的强引用。覆盖槽会释放旧所有者并可能排队旧对象的终结器，因此槽覆盖不会留下悬空所有权。

可信宿主 API（尤其是 `SetHostResurrectionPolicy`）不能被暴露给不可信脚本。

## 最短反例

`eventsearch` 对固定三线程程序做广度优先搜索：

- `T0`（脚本）：发票据、调用
- `T1`（宿主）：分配、强引用释放、撤销、地址复用
- `T2`（终结器）：执行 `Finalize`

搜索目标不是“运行过终结器”，而是撤销事件之后出现一次真实成功的 `Call(resource)`。

正确模型在全部有界交错中无结果。待审脆弱实现的最短路径为 6 个事件：

1. `T1 Allocate`：对象 A 绑定 resource 1。
2. `T0 IssueTicket`：票据绑定 `capGen=0`。
3. `T1 StrongDrop`：强引用降到 0，进入 `finalizing`，排队终结器。
4. `T1 Revoke`：`capGen` 变为 1，resource 1 被清除。
5. `T2 Finalize`：回调内 `WeakGet` 被拒绝，但待审实现无视宿主策略和撤销状态，记录两个复活请求，恢复旧资源并把 A 放回 `g1`、`g2`。
6. `T0 Call`：待审实现的 `Call` 漏检能力代次和撤销位，真实调用 resource 1 成功。

运行：

```sh
go run ./cmd/eventsearch
```

## 覆盖与验证

测试覆盖：

- 终结回调内 `WeakGet` 必须失败；
- 两个复活请求，只有首个可被接受；
- `Revoke` 在 `Finalize` 回调前后两个并发顺序；
- 全局槽覆盖旧对象并排队旧对象终结；
- `dead` 地址复用后旧弱引用和旧票据失效；
- 重复 `StrongDrop` 不重复排队终结器；
- 安全实现资源调用计数为 0，待审实现为 1。仅析构次数本身不会被判为漏洞。

```sh
go test ./...
go test -race ./...
go vet ./...
```
