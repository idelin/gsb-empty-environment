# diskpq — 磁盘优先、带稳定序号的优先队列

纯标准库实现。任务持久化在**调用方指定的目录**里；支持优先级更新、删除、
原子批量弹出；相同优先级严格按入队序（FIFO），重启后顺序不变；优先级
允许负数。

## 快速开始

```go
q, _ := diskpq.Open("/var/lib/myqueue")
defer q.Close()

q.Enqueue("job-1", 5)           // 优先级可负，越小越先弹出
q.Enqueue("job-2", 5)           // 同优先级按入队序
q.Update("job-2", -1)           // 更新优先级；任务不存在返回 (false,nil)
q.Delete("job-1")               // 删除；任务不存在返回 (false,nil)

batch, err := q.PopBatch(100)   // 原子批量弹出，空队列返回 (nil,nil)
```

运行测试与样例：

```sh
go test -v ./...
go run ./cmd/sample            # 可加参数指定数据目录
```

## 存储格式（全部位于指定目录）

| 文件 | 说明 |
| --- | --- |
| `wal.log` | 只追加 WAL，每行一条 `crc32c json` 记录：enq/upd/del/pop |
| `snapshot.json` | 全量快照（活跃任务 + 最后序号），带 CRC，超阈值自动压缩 |
| `snapshot.json.tmp` | 快照发布前的临时文件，rename + 目录 fsync 原子生效 |

## 关键设计

- **WAL 先行**：任何修改先追加记录并 `fsync`，成功后才改内存并返回。
- **稳定序号**：`Seq` 单调递增不回收，写入每条 enq 记录与快照 `last_seq`；
  堆按 `(Priority, Seq)` 排序，同优先级 FIFO，重启/压缩后完全一致。
- **原子批量弹出**：整批只提交一条 pop 记录；提交前候选留在堆与索引中，
  提交失败立即回滚内存并把未持久化字节截断回滚，任务一条不丢；提交成功后
  重启恰好消失一次，不重复不复活。
- **更新不能复活已删任务**：API 与 WAL 重放都只对“当前存活”的 id 生效，
  针对已删除/不存在 id 的旧 upd 记录一律忽略。
- **撕尾恢复**：断电导致的半截行或 CRC 不符记录视为未提交尾部，重放时
  丢弃并截断；已提交前缀不受影响。
- **故障注入点**：`Hooks{BeforeWALAppend, BeforeWALSync, AfterWALSync,
  BeforeSnapshotRename}`，测试用它模拟 fsync 失败、崩溃时刻。

## 崩溃恢复与断电边界

保证：

1. 方法返回 nil 后进程崩溃 / kill -9：记录已 fsync，重放恢复。
2. fsync 与改内存之间崩溃：以磁盘为准，重放已持久化记录。
3. 断电撕坏 WAL 尾部：CRC 检测，只应用完整前缀并截断。
4. pop 事务：fsync 前崩溃任务全在；fsync 后崩溃整批恰好消失；返回错误则
   全部任务仍在队列中。
5. 快照：临时文件写满+fsync，再 rename + 目录 fsync，崩溃只可能留下新旧
   其中一份，不会读到半写快照。

不保证（边界）：

- 单机持久化，无复制；目录所在磁盘整体损坏/丢失则队列丢失。
- `fsync` 只兑现 OS/文件系统契约；硬件撒谎（易失写缓存忽略 flush）时真断电
  可能丢已确认写入。
- 目录 fsync 的原子 rename 依赖本地 POSIX 文件系统；网络/overlay 等语义可能
  更弱，`Open` 不做检测。
- CRC 检测撕裂与位翻转，不防恶意损坏；快照损坏直接报错而非静默清空。
- 弹出任务后的下游副作用需要调用方用 `Item.ID/Seq` 自行做幂等，本包只保证
  队列侧 exactly-once 状态。
