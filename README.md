# MVCC 版本链管理与垃圾回收（GC）

纯 Go 标准库实现：多版本链管理、ReadView 可见性判断、基于全局最小活跃
ReadView 的废弃版本修剪。

## 设计

- **版本链**：每个数据项一条单链表，新版本插在表头，按时间戳倒序（新→旧）。
- **可见性**：读操作携带 ReadView（读时间戳 `readTS`），沿链表返回第一个
  `ts <= readTS` 的版本。
- **ReadView 追踪**：`BeginRead` 注册活跃读事务，`EndRead` 释放；
  `MinActiveReadTS` 返回全局最小活跃读时间戳。
- **GC 修剪**：找到"最老活跃读者仍可见的最旧版本"（最后一个
  `ts <= minActiveReadTS` 的版本），截断其后所有更旧版本——它们已被覆盖
  且对任何活跃读者不可见，断链后由 Go 运行时回收内存。无活跃读者时仅保留
  最新版本。

## 目录

- `mvcc/mvcc.go` — 核心实现（`Store` / `VersionChain` / `Version` / `ReadView`）
- `mvcc/mvcc_test.go` — 单元测试（含验收边界场景）
- `cmd/demo/main.go` — 可运行样例

## 运行

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行验收场景样例
```

## 验收场景

数据项 X 有版本 v1(t=1)、v2(t=5)、v3(t=10)；活跃 ReadView 为 t=8 与 t=12，
全局最小活跃 ReadView = t=8。GC 修剪 v1（被 v2 覆盖且对 t=8 不可见），
保留 v2、v3；GC 前后 Read(t=8)→v2、Read(t=12)→v3 结果不变。
