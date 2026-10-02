# quota — 分布式滑动窗口限流：心跳租约 + 配额预分配（Go 标准库实现）

中心协调器（Master）持有全局限流池（如 3000 QPS），以**租约（lease）**形式把配额切片分发给各工作节点（Worker）。Worker 在本地直接消耗预分配的 Local Token，**无需任何网络 RPC**；当本地余额低于水位线时，**异步**向 Master 发起**批量心跳续订**，把多次续订合并成一次 RPC 往返，降低网络开销。Master 收到心跳后先动用空闲池，不足时按"最空闲优先"从其他 Worker 回收未使用的额度再分配。

## 结构

| 文件 | 职责 |
|---|---|
| `master.go` | 协调器：租约台账、公平初分、空闲回收再分配、租约过期回收 |
| `worker.go` | 工作节点：本地令牌消耗、低水位异步批量续订、租约过期 fail-closed、召回对账 |
| `rpc.go` | 传输层：`net/http`+`encoding/json` 的 TCP RPC，以及进程内直连传输 |
| `cmd/demo` | 验收场景的可运行样例 |

## 关键设计

- **滑动窗口 = 租约 TTL**：配额按窗口（`window`）授予，过期未续订的租约由 Master 回收，令牌返回全局池；Worker 侧租约过期后 `Allow` fail-closed，直到续订成功。
- **批量预分配**：Worker 余额降至上次授予量的 20%（可配）时触发一次异步心跳，批量申请 `batchSize` 个令牌；任意时刻最多一个续订在途。
- **增量授权 + 召回对账**：心跳响应携带 `Additional`（本次新增额度，无竞态）与 `Remaining`（Master 视角的租约余额）。若 Master 为更忙的节点回收了本节点额度，Worker 下次心跳时按 `Remaining` 钳制本地余额，集群最终收敛到全局上限。
- **回收策略**：仅从其他 Worker 的**未使用部分**（`granted - used`）回收，按"已用量最少者优先、余额大者优先"排序；已消耗的令牌永不被回收。Master 全程保证 `Σ granted ≤ 池总量`。

## 运行样例

```console
$ go run ./cmd/demo            # 进程内传输（默认）
$ go run ./cmd/demo -transport http   # 真实 TCP 套接字（HTTP/JSON）
```

输出：

```
== initial fair split (3000 QPS / 3 workers) ==
  worker A: granted= 1000 used=    0
  worker B: granted= 1000 used=    0
  worker C: granted= 1000 used=    0
  total granted=3000 (pool cap 3000)

== A bursts to 1500 QPS (local share is only 1000) ==
A consumed 1500 tokens locally, renewals (RPC round-trips): 3

== leases after rebalancing ==
  worker A: granted= 1600 used= 1400
  worker B: granted= 1000 used=  200
  worker C: granted=  400 used=    0
  total granted=3000 (pool cap 3000)
master served 4 heartbeats total for ~1700 tokens moved
```

A 突发 1500 QPS 耗尽本地 1000 配额后，仅 3 次批量心跳（每次 200 令牌）便从空闲的 C 调拨 600 额度（C: 1000→400，A: 1000→1600），B 不受影响，全局不变量 `Σgranted = 3000` 始终成立；约 1700 个令牌的转移只产生 4 次 RPC。

## 测试

```console
$ go test -race ./...
ok  	quota	1.5s
```

覆盖：三节点平分 3000 QPS、A 突发从 C 调拨（验收场景，进程内与 TCP 两种传输）、本地消耗零 RPC、低水位异步批量续订、续订失败用量回滚、租约过期双向行为、已用令牌不可回收、并发心跳不超发（`-race` 验证）。

> 注：TCP 集成测试在禁止绑定监听端口的沙箱中会自动 skip；`TestBurstRebalancesAcrossCluster` 以进程内传输覆盖同一场景。
