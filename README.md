# Causal Consistent Slice over a Multi-Object Replicated Store

在带**因果标签**的多对象副本上选取一个**因果一致切片**（consistent slice）。
每个版本显式携带其因果前驱；选择器保证：**切片里不能读到“后果”却缺少“原因”**。
当所需版本尚未到达时，请求被标记为**等待**，并返回阻塞依赖；网络、故障、时间全部由
一个**单进程离散事件模拟器**控制。仅使用 Go 标准库。

## 目录结构

- `causal/` — 版本/依赖登记、版本向量（VV）、纯函数切片选择器 `Select`。
- `sim/` — 单进程事件模拟器：事件堆、副本、gossip 复制、丢包/分区/宕机故障、等待者。
- `cmd/demo/` — 端到端运行样例（“有 B 无 A → 阻塞 → 补齐 → 激活”）。
- 测试全部在 `go test` 中（`causal/selector_test.go`、`sim/sim_test.go`）。

## 模型

- 每个对象的版本是每对象连续序号 `object@seq`（从 1 开始）。
- `Registry.Stamp(obj, deps)` 生成新版本；`deps` 是跨对象因果前驱，同对象前一版本
  自动连入，因此依赖 DAG 同时表达“跨对象因果闭包”和“每对象前缀闭包”。
- 副本持有两个集合：
  - `Present`：已送达、可读的版本；
  - `Known`：经 gossip digest 广告而知“存在”但尚未送达的版本（用来给“读最新”定界）。
- 请求 `Request`：
  - `Target VV`：钉死每对象想要的精确版本；
  - `Latest []string`：读这些对象“本副本已知的最新版本”。
  - 两者对同一对象互斥。
- 结果 `Selection`：
  - `Selected`：仅含被请求对象的版本向量；
  - `Closure`：连同因果依赖一并展开的完整向量（同一版本集合）；
  - `Blocking`：每个无法满足的对象给出前沿版本 `Want` 与一个根因 `Cause`；
  - `Complete`：是否无阻塞。

## 一致性规则（安全性）

`Select` 是 `(registry, view, request)` 的纯函数，返回**受请求上界支配的最大一致切片**：

1. 切片中每个版本都 `Present`，且其传递依赖全部在切片中——**有 B（写依赖 A）就必须有对应 A**。
2. 被请求对象不得超过自身上界（钉死值或已知最新）。
3. 某版本的闭包若越过**另一个被请求对象**的钉死上界，即使该版本本地齐全也不能纳入，
   选择器把前沿下调到最大可纳入前缀，并把被卡住的版本记入 `Blocking`。
4. 缺失版本 → `Blocking` 中标等待，绝不返回半成品切片。

实现上从各上界出发，对“闭包不可交付 / 越过请求上界”的对象做**单调下降**，
分量只减不减、下界为 0，有限步到达**最大不动点**。

## 模拟器（通信 / 故障 / 时间）

`sim.Simulator` 用 `container/heap` 维护单一事件队列，没有 goroutine、真实网络或墙钟：

- 副本周期性 gossip digest，缺版本方发 `request`，持有方回 `push`（三段式反熵）。
- 故障接口：`Partition/Heal`（链路双向断开/恢复）、`Down/Up`（副本宕机/重启）、
  `AddRule`（按消息丢包）、`FilterPush`（按版本过滤，可精确制造“只收到 B 收不到 A”）。
- `Read` 一次性求值；`Wait` 注册独立等待者，每次送达后重估，满足时回调一次。
- 每次读/等都对副本 `Present/Known` 做拷贝快照，**不同请求不共享任何可变快照状态**。

## 安全性与活性证据

- **安全性**由测试强制：
  - 单元测试覆盖 B 依赖 A、前缀缺口、闭包展开、最大切片、钉死越界、已知未送达、
    请求隔离、确定性顺序，以及两个专门的回归测试
    （`TestPinCapVsCausalCause`、`TestCausalClosureNoSeqPruning`）。
  - `TestRandomSafety` 跑 40 条随机历史（随机写/因果依赖、随机延迟与丢包、两次
    随机分区、随机一次性读），对每个观测切片机器校验：
    (1) 所选版本均在场；(2) 依赖闭包（无后果无原因）；(3) 不越请求上界；
    (4) 在请求上界内不存在更大的一致超集（极大性）；(5) `Complete` 标志与阻塞集一致。
- **活性**：
  - `TestLivenessBlockedThenSatisfied`：分区期间 r2 只收到 B@1 而缺 A@1，等待者保持
    挂起且只暴露空切片；链路恢复且故障停止后，gossip 在有界模拟时间内补齐 A@1，
    等待者恰好满足为 `{A:1 B:1}`。
  - `TestWaiterIndependence`：两个等待者互不干扰，一个满足不污染另一个。
  - `TestRandomSafety` 末尾在“所有链路愈合 + 关闭丢包”后断言每个副本最终收敛到完整 VV。
  - 论证：依赖图是有限 DAG；反熵周期性重传 digest，故在链路最终连通且不再丢包的
    公平调度下，每个已生成版本经有限次 gossip/request/push 到达所有副本，等待者
    随之满足。分区/宕机/持续丢包期间不保证进展（正确地持续等待）。

## 运行

```sh
go run ./cmd/demo
go test ./...
```

### 样例输出（`go run ./cmd/demo`）

```
  t=10   write          A@1 at r1 deps=[]
  t=20   write          B@1 at r1 deps=[A@1]
  t=39   deliver        r2 <= r1 items=[B@1]
  t=45   partition      r1 <X> r2
  ---- r2 one-shot read at t=55 ----
  selected VV : {}
  closure  VV : {}
  block       : A@1 waits-for A@1 (missing)
  block       : B@1 waits-for A@1 (missing)
  t=55   wait           demo-wait on r2 wants latest of [A B] / {}
  t=150  heal           r1 <-> r2
  t=159  deliver        r2 <= r1 items=[A@1]
  t=159  wait-satisfied demo-wait slice={A:1 B:1}
  ---- result after heal ----
  waiter complete: true   served slice VV: {A:1 B:1}
  one-shot read at t=55: complete=false blocking=2
```

解读：r2 先收到了 B@1（其写依赖 A@1），但 A@1 被故障扣下；此时任何含 B 的切片都
不合法，于是一次性读返回空切片并给出 `B@1 waits-for A@1`，等待者挂起。链路恢复后
A@1 到达，等待者得到 `{A:1 B:1}`。

### 测试结果（`go test ./...`）

```
ok  	causalslice/causal
?   	causalslice/cmd/demo	[no test files]
ok  	causalslice/sim
```

详细用例（`go test -v`）：causal 包 10 个、sim 包 3 个，全部 PASS，
其中 `TestRandomSafety` 为 40 条随机故障历史的安全性 + 最终收敛校验。
