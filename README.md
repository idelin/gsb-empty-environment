# splitter — 本地双目标分流器

业务记录软删除后路由到归档库、可恢复、可乱序更正的本地参考实现。
两个目标（在线 / 归档）由可注入的文件适配器模拟，跨目标迁移通过
**持久化意图日志 + 原子版本指针** 实现崩溃安全。

## 数据模型

- 事件：`{key, version, content, is_deleted}`，`version` 单调递增。
- 未删除版本路由到 `online`，删除版本路由到 `archive`。
- 每个主键只接受更高版本：
  - 同版本同内容 → 幂等 no-op；
  - 同版本不同内容（或删除标记不同）→ 冲突，拒绝；
  - 更低版本（含延迟到达的删除）→ 丢弃，**永远不会复活旧归档**。

## 目录布局

```
DATA/
  online/      在线目标（FileAdapter，tmp+rename+fsync）
  archive/     归档目标（FileAdapter）
  pointers/    每主键一个版本指针，原子提交（tmp+rename+fsync dir）
  journal/     意图日志 intents.jsonl（WAL，每条 fsync）
  snapshots/   活动快照钉住的指针集合
```

## 迁移协议（跨目标无法原子，用意图 + 确认进度）

`Apply` 对每个被接受的事件按以下顺序推进，每一步都先落日志再动作：

1. `INTENT`：持久化意图（含完整内容，目标写成功但回执丢失也能重放）。
2. 写入新位置 blob（幂等 Put）→ 记 `COPIED`。
3. 原子提交版本指针（仅当仍高于已提交版本）→ 记 `COMMITTED`。
4. 跨目标时删除旧位置副本（被活动快照钉住则跳过，留给 GC）。
5. 日志标记完成并压缩。

**查询只读指针**：读者永远只看到一个已确认位置，迁移中途两侧
都有副本也不影响读结果。

## 崩溃恢复矩阵（`Open` 自动执行 `Recover`，幂等可重复）

| 崩溃点 | 磁盘状态 | 恢复行为 |
|---|---|---|
| 意图后、blob 写入前 | 只有 INTENT | 用日志中的内容重写 blob，继续走完 |
| blob 写完、指针提交前（回执丢失） | 新位置有副本 | 幂等重 Put，提交指针，清理旧副本 |
| 指针提交后、日志跟进前 | 两侧都有副本 | 指针已是目标版本，仅补做旧副本清理 |
| 清理中途 | 旧副本可能残留 | 删除幂等，重放清理后完成 |
| 重复恢复 / 重复重放 | — | 版本仲裁保证幂等，最终状态不变 |

任何情况下最终位置都与**最后一个合法事件**一致。

## 垃圾回收

`GC` 只删除**没有任何当前指针、也没有任何活动快照引用**的旧副本。
`BeginSnapshot`/`EndSnapshot` 持久化钉住的指针集合，重启后仍生效。

## 使用

```sh
# 重放事件日志（JSONL），重复执行幂等收敛
go run ./cmd/splitter -dir DATA replay examples/events.jsonl

# 指针视角的全量扫描（每键最高已确认版本）
go run ./cmd/splitter -dir DATA scan

# 参考实现：直接扫两个目标库取每键最高版本，用于交叉校验
go run ./cmd/splitter -dir DATA scan -reference

# 按键查询（只走指针确认的位置）
go run ./cmd/splitter -dir DATA get order-1

# 垃圾回收
go run ./cmd/splitter -dir DATA gc
```

事件行格式：`{"key":"order-1","version":1,"content":"...","is_deleted":false}`

## 测试

```sh
go test ./...
```

故障注入通过 `FaultAdapter`（包裹目标，before/after 钩子可返回错误
或 panic 模拟掉电）与 `Hooks`（指针提交后 / 清理前后）实现，覆盖：
路由与版本仲裁、删除迁移与恢复、延迟删除不复活、上述全部崩溃点、
回执丢失重试、重复恢复、GC 与快照钉住、指针扫描与参考扫描一致性。
