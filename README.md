# leasesim — 租约领导 + 单调 fencing token 的事件模拟

仅用 Go 标准库实现。通信、故障与时间全部由单进程事件模拟器控制（虚拟时钟 +
确定性事件堆），时钟偏差通过显式模型（`drift`/`offset`/进程暂停时时钟冻结）
进入系统。核心论点：**租约不是强隔离**，真正的隔离由资源端校验的单调
fencing token 提供。

## 结构

- `sim.go` — 事件模拟器：虚拟时间、事件全序、证据日志
- `clock.go` — 显式时钟模型：走速/相位偏差、暂停期间时钟冻结
- `net.go` — 确定性网络：固定延迟 + 可指定的滞留/乱序消息
- `lease.go` — 租约授予者：单持有者，每次授予发放严格递增 token
- `resource.go` — 资源端：只接受严格递增 token，重复与回退均拒绝
- `node.go` — 领导候选人：用本地（可能有偏差的）时钟判断租约有效性
- `scenario.go` / `main.go` — 故障转移场景与证据输出
- `sim_test.go` — 安全性与活性测试

## 运行

```
go run .     # 打印事件日志 + 安全性/活性证据
go test ./... 或 go test -v ./...
```

## 运行样例（go run .）

```
=== 事件日志 (单进程事件模拟器, 虚拟时间) ===
[t=   10ms] grantor: 授予 A 租约 token=1 (本地时钟过期时刻 t=510ms)
[t=   20ms] A: 成为领导, token=1
[t=   30ms] A: 本地时钟 t=30ms 判定租约有效, 发出写 token=1 value="A#1"
[t=   30ms] A: 写 "A-delayed" (token=1) 进入网络, 但将被滞留 800ms
[t=   40ms] A: 进程暂停 1000ms (GC stop-the-world)
[t=   40ms] resource: 接受 A 的写 token=1 value="A#1"
[t=  610ms] grantor: 授予 B 租约 token=2 (本地时钟过期时刻 t=1110ms)
[t=  620ms] B: 成为领导, token=2
[t=  630ms] B: 本地时钟 t=630ms 判定租约有效, 发出写 token=2 value="B#2"
[t=  640ms] resource: 接受 B 的写 token=2 value="B#2"
[t=  650ms] B: 写 "B#2" (token=2) 已提交
[t=  830ms] resource: 拒绝 A 的写 token=1 value="A-delayed" (已见 token=2, 重复/回退非法)
[t= 1040ms] A: 恢复运行, 本地时钟读数 t=40ms (真实 t=1040ms, 偏差 -1000ms)
[t= 1100ms] A: 本地时钟 t=100ms 判定租约有效, 发出写 token=1 value="A-stale"
[t= 1110ms] resource: 拒绝 A 的写 token=1 value="A-stale" (已见 token=2, 重复/回退非法)
[t= 1120ms] A: 写 "A-stale" (token=1) 被资源拒绝

=== 安全性证据 ===
已接受写的 fencing token 序列: [1 2] — 严格递增: true
已拒绝: A 的写 token=1 value="A-delayed" (t=830ms) — 旧任期/重复写被拦截
已拒绝: A 的写 token=1 value="A-stale" (t=1110ms) — 旧任期/重复写被拦截
资源最终值: "B#2" — 来自最高 token, 未被旧消息覆盖
A 凭本地时钟认定租约有效并发写 2 次, 其中租约实际已过期的误判 1 次 — 租约判断失效, 但资源未受影响

=== 活性证据 ===
故障转移完成: B 的写 "B#2" (token=2) 于 t=640ms 提交
从旧租约过期 (t=510ms) 到新领导提交 (t=640ms) 耗时 130ms — 系统在旧领导瘫痪后恢复服务
B 已提交的写: [B#2]
```

## 测试结果（go test -v ./...）

```
--- PASS: TestStaleLeaderWriteRejectedAfterResume   旧领导暂停恢复后的写被资源拒绝
--- PASS: TestDelayedOldMessageCannotOverwrite      新领导成功后滞留旧消息不能覆盖
--- PASS: TestDuplicateTokenRejected                token 重复(相等)非法
--- PASS: TestRegressiveTokenRejected               token 回退(更小)非法
--- PASS: TestFencingTokenMonotonicInvariant        已接受 token 序列严格递增
--- PASS: TestLeaseAloneIsNotIsolation              时钟偏差使租约误判, 资源仍隔离
--- PASS: TestLivenessNewLeaderCommits              故障转移在有界时间内完成
--- PASS: TestMonotonicityUnderRandomChaos          50 组随机延迟/暂停/竞争下不变量成立且持续有提交
PASS
ok  	leasesim
```

## 论证要点

- **安全性**：资源端不变量 —— 已接受写的 fencing token 严格递增
  （`token <= lastToken` 即拒绝，重复与回退同属非法）。旧领导暂停后恢复、
  旧消息滞留后到达，都因 token 过期而被拒，与租约/时钟状态无关。
- **时钟偏差显式建模**：节点用本地时钟比较授予者时钟域的过期时刻；进程暂停
  建模为本地时钟冻结（`Clock.Freeze`），恢复后产生 -1000ms 偏差，导致旧领导
  误判租约有效 —— 这正是"租约不能当强隔离"的具体机制。
- **活性**：租约只在授予者时钟上过期后才重新授予，因此旧领导瘫痪只造成
  有界停顿（样例中 130ms），新领导随后获得更高 token 并完成写。
