# cdcsched — 多表级联依赖拓扑排序与外键保序器

跨表 CDC 变更重放调度器。根据表间外键依赖关系（如 `orders → users`、
`order_items → orders`），在并发向目标库重放写入时严格保证：

- **INSERT/UPDATE**：父行先写入，子行后写入；
- **DELETE**：子行先删除，父行后删除。

仅使用 Go 标准库。

## 组成

| 文件 | 职责 |
| --- | --- |
| `schema.go` | 表/外键元数据；表级 Kahn 拓扑排序（父先）与删除序（子先），含环检测 |
| `event.go` | CDC 事件模型（Insert/Update/Delete，主键 + 行镜像） |
| `scheduler.go` | 事件级依赖 DAG 构建 + 并发调度器（核心） |
| `targetdb.go` | 模拟目标库，强制执行外键约束，用于演示与测试 |
| `cmd/demo/main.go` | 可运行样例 |

## 工作原理

1. `Schema.TopoSort()` 对表做拓扑排序，得到父表在前的插入序；`DeleteOrder()` 为其逆序。
2. `Scheduler.Replay(events, apply)` 对同一批次事件构建**行级**依赖 DAG：
   - 子行 Insert/Update 依赖同批次中其外键引用的父行 Insert/Update；
   - 父行 Delete 依赖同批次中所有引用它的子行 Delete（取子行 before-image 的外键列）。
3. 调度器用 worker 池并发执行入度为 0 的事件；依赖未满足的事件被**挂起**，
   依赖成功后自动放行；某事件失败时，其全部（传递）下游事件被跳过而不是
   盲目重放，避免目标库外键约束报错。

## 运行

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行演示
```

演示中 CDC 批次乱序到达（`order_items` 先于 `users`），调度器自动挂起子行，
实际落库顺序为 `users → orders → order_items`；删除批次则按相反顺序执行，
全程 0 外键错误。
