# leasearb — 带代次的租约写入仲裁器

资源可续租或转交；新持有者开始工作后，旧持有者的请求仍可能迟到。
本库用**代次（generation / fence token）**仲裁写入，并严格区分：

- **请求重复（DUPLICATE）**：请求 ID 已被处理过，幂等返回首次结果，不重复应用；
- **过期持有者（STALE）**：携带旧代次的写入被栅栏（fencing）拒绝；
- **租约过期（EXPIRED）**：代次仍是当前值但租约已失效，需先续租；
- **续租不复活**：`Renew` 携带旧代次时返回 `ErrStaleGeneration`，绝不复活旧代次。

## 结构

- `leasearb/arbiter.go` — 仲裁器：`Acquire`（获取/转交，代次 +1）、`Renew`、`Write`、`Snapshot`
- `leasearb/clock.go` — `ManualClock`：只随 `Advance` 前进，保证脚本可确定性重放
- `leasearb/script.go` — 事件脚本 DSL 与运行器
- `scripts/*.script` — 三个场景脚本：时钟前跳、延迟确认、交接并发
- `cmd/leasearb-demo` — 重放全部脚本并打印带时间戳的轨迹

## 脚本 DSL

```
lease 10s                        # 设置租约 TTL（重置状态）
tick 30s                         # 时钟前跳
acquire orders alice             # 获取/转交，打印新代次
renew orders alice 1             # 续租（旧代次报错）
write orders alice 1 req-1 pay   # 仲裁写入（请求 ID 幂等）
expect STALE fenced              # 断言上一步结果（可带子串）
snapshot orders                  # 打印持有者/代次/已应用写入
```

## 运行

```sh
go test ./...                 # 单测 + 脚本重放测试
go run ./cmd/leasearb-demo    # 打印三个场景的完整轨迹
```

## 关键设计

- 确认表（acks）**跨代次保留**：已应用写入的迟到重试判为 DUPLICATE 而非 STALE；
- 判定顺序：先查重（请求 ID），再查代次（栅栏），再查持有者与租约有效期；
- 过期但未被接替的租约允许续租（代次仍当前，重新锚定过期时间）；
- 所有操作在互斥锁下串行化，交接竞争由代次计数器定胜负。
