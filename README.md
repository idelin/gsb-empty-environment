# GNUnet ego根标签与委派链解析

GNUnet 的 GNS 以本地 ego 作为解析起点，远端区的记录若能改变根标签的含义，就能把用户引向攻击者的区。给Go解析器写供名称解析调用的委派包，以下为本题规定：名称从右向左，最右标签只在本地 ego 表里查找（名称到区公钥），不得用任何网络区的记录解释；其余标签在当前区里查记录，PKEY 记录把解析切到另一个区；委派深度最多 8 层，出现重复区公钥视为环而失败；绝对过期时间不晚于当前时间的记录跳过；同一标签下 PKEY 与 CNAME 并存，整个区块判无效；被删除的 ego 对应的名称立即不可解析。用远端区声明同名根标签、深度与环、过期记录、PKEY 与 CNAME 冲突、ego 删除验证。

## 包结构

- `EgoTable`：本地根标签 → 区公钥表，是最右标签唯一允许的来源；`Delete` 后该名称立即返回 `ErrEgoNotFound`。
- `ZoneStore` / `MemoryZoneStore`：按区公钥取区块的接口与内存实现（生产环境可替换为网络获取实现）。
- `Resolver.Resolve(name)`：从右向左逐标签解析，返回 `Result{Name, Zone, Chain}`。

## 解析规则

- 最右标签只查 `EgoTable`，在任何区记录查询之前完成；远端区无法声明或覆盖根标签。
- PKEY 切区：最多跟随 `MaxDelegationDepth`(8) 次；链上出现重复区公钥立即报 `ErrDelegationLoop`。
- CNAME：以别名重新开始解析，受 `MaxCNAMERedirects`(8) 限制以防不自终。
- 记录 `Expire` 为绝对时间，`!Expire.After(now)` 即跳过；零值表示永不过期。
- 同一标签同时存在存活的 PKEY 与 CNAME 时整个区块无效（`ErrConflictingRecords`）；过期记录不参与冲突判断。

## 用法

```go
egos := netsec.NewEgoTable()
store := netsec.NewMemoryZoneStore()
r := netsec.NewResolver(egos, store, nil) // nil 表示 time.Now

_ = egos.Put("example", rootKey)
res, err := r.Resolve("v1.docs.www.example")
```

运行测试：`go test ./...`（另可用 `go test -race` 验证并发删除场景）。
