# leasearb — 带代次的租约写入仲裁库

资源租约可以续租或转交；新持有者开始工作后，旧持有者的请求（在途写入、
重试、延迟确认）仍可能返回。本库用**代次（fencing generation）+ 请求去重**
做写入仲裁，严格区分"重复请求"与"过期持有者"，并保证续租不能复活旧代次。

## 核心语义

- 每次授予（`Acquire` / `Transfer`）代次单调 +1，即使租约过期也永不复用。
- 写入携带 `(holder, generation, requestID)`，仲裁结果 `Verdict`：

| 判定 | 含义 |
|---|---|
| `Accepted` | 首次见到该 requestID，写入被接纳 |
| `Duplicate` | 同一 `(generation, requestID)` 已应用过 —— 重试/延迟确认，幂等成功 |
| `StaleGeneration` | 代次落后 —— 旧持有者的在途写入，拒绝（fencing） |
| `NotHolder` | 代次正确但持有者不符，拒绝 |
| `LeaseExpired` | 代次正确但时钟已越过到期点，拒绝 |

- 判定顺序：**先查去重表，再查代次**。因此交接后旧请求的延迟确认返回
  `Duplicate`（幂等），而不是误报为过期持有者。
- `Renew` 要求代次与当前完全一致；旧代次的续租返回 `ErrStaleGeneration`，
  不能复活旧持有者。

## 快速开始

```go
clk := leasearb.NewManualClock(time.Now()) // 或 nil 使用系统时钟
arb := leasearb.NewArbiter(clk)

l1, _ := arb.Acquire("alice", 10*time.Second)   // gen=1
arb.AdmitWrite("alice", 1, "req-1")             // Accepted
arb.Transfer("alice", "bob", 10*time.Second)    // gen=2
arb.AdmitWrite("alice", 1, "req-1")             // Duplicate（延迟确认）
arb.AdmitWrite("alice", 1, "req-2")             // StaleGeneration（在途写入）
arb.Renew("alice", 1, 10*time.Second)           // ErrStaleGeneration（拒绝复活）
```

## 可重放事件脚本

`scripts/demo.events` 用确定性手动时钟演示三个场景：时钟前跳、延迟确认、
交接并发。重放运行：

```sh
go run ./cmd/replay scripts/demo.events
```

脚本 DSL（`#` 为注释）：

```
clock +30s                  推进手动时钟（模拟时钟前跳）
acquire <holder> <ttl>      申请租约
renew <holder> <gen> <ttl>  续租
transfer <from> <to> <ttl>  显式交接
write <holder> <gen> <req>  提交写入仲裁
expect-lease <holder> <gen> / expect-no-lease / expect-write <req> <判定>
```

## 测试

```sh
go test ./...
```
