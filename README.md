# LSM MemTable 分层转换器(WAL + 刷盘状态机)

纯 Go 标准库实现的 LSM 树内存表持久化组件:

```
                达到 4MB 阈值, O(1) 轮转              后台单线程, FIFO 顺序刷盘
┌──────────┐  ───────────────────────────────▶ ┌───────────────────┐  ─────────────────────────▶ ┌───────────────┐
│ MemTable │                                    │ Immutable MemTable │                             │ L0 SSTable(s) │
│ + WAL 段 │ ◀── 派生新 MemTable + 新 WAL 段 ── │  (刷盘队列, 排队中) │                             │  旧 WAL 段删除 │
└──────────┘                                    └───────────────────┘                             └───────────────┘
```

## 设计要点

- **轮转(rotation)**: MemTable 近似大小达到阈值(默认 4MB)时, 冻结为
  Immutable MemTable 并进入 FIFO 刷盘队列, 同时派生新 MemTable 与新 WAL 段
  接收写入。轮转只涉及指针交换与建文件, 前台写路径不等待刷盘。
- **刷盘(flush)**: 后台唯一刷盘协程按 FIFO 顺序把 Immutable MemTable 写成
  Level 0 SSTable(数据区 + 稀疏索引 + 脚注魔数, 临时文件 fsync 后原子改名),
  落盘后才删除对应旧 WAL 段, 完成 WAL 安全轮转。
- **WAL**: 每个 MemTable 绑定一个段, 记录带 CRC32, 崩溃后按段序号回放;
  多段残留时旧段恢复为 Immutable 重新排队刷盘, 最后一段恢复为活跃表。
- **读路径**: 活跃表 → Immutable(新→旧) → L0 SSTable(新→旧, 稀疏索引二分 +
  单块 ReadAt)。支持墓碑删除。
- **耐用性**: 轮转与关闭时 fsync WAL; SSTable fsync 后才删旧 WAL;
  `Options.SyncWrites` 可开逐条 fsync。

## 文件

| 文件 | 说明 |
|---|---|
| `wal.go` | 预写日志段: 追加写、CRC 校验、回放 |
| `memtable.go` | 可变/不可变内存表, 大小估算, 排序快照 |
| `sstable.go` | SSTable 写入(原子改名)与稀疏索引读取 |
| `engine.go` | 状态机: 轮转、后台单线程刷盘调度、恢复、读写路径 |
| `engine_test.go` | 验收测试 |
| `cmd/demo/main.go` | 运行样例 |

## 运行

```sh
go test -v ./...   # 运行验收测试
go run ./cmd/demo  # 运行样例(写入 12 万条约 16MB, 触发 4 次 4MB 轮转)
```

## 测试结果(go test -v ./...)

```
=== RUN   TestRotationAndSequentialFlush      # 两次 4MB 轮转 → 2 个 Immutable 顺序刷入 L0, 7 万条全量校验
--- PASS: TestRotationAndSequentialFlush (6.56s)
=== RUN   TestForegroundWritesNotStalled      # 刷盘被人为阻塞时, 写入与二次轮转照常, 前台无停顿
--- PASS: TestForegroundWritesNotStalled (1.10s)
=== RUN   TestWALRecoveryActiveSegment        # 崩溃后活跃 WAL 段回放恢复
--- PASS: TestWALRecoveryActiveSegment (0.51s)
=== RUN   TestWALRecoveryWithImmutable        # 轮转后未刷盘即崩溃, 双 WAL 段恢复并重刷
--- PASS: TestWALRecoveryWithImmutable (0.70s)
=== RUN   TestCloseFlushesActiveMemtable      # 正常关闭刷空活跃表, WAL 全部轮转清理
--- PASS: TestCloseFlushesActiveMemtable (0.16s)
=== RUN   TestDeleteTombstone                 # 墓碑删除跨刷盘/重启生效
--- PASS: TestDeleteTombstone (0.76s)
=== RUN   TestSSTableRoundTrip                # SSTable 读写往返与稀疏索引
--- PASS: TestSSTableRoundTrip (0.04s)
PASS
ok      example.com/lsm
```

## 运行样例(go run ./cmd/demo)

```
writing 120000 records (4MB memtable threshold)...
[monitor] rotations=1 flushes=0 queue=0 l0files=0
[monitor] rotations=1 flushes=1 queue=0 l0files=1
[monitor] rotations=2 flushes=1 queue=0 l0files=1
[monitor] rotations=2 flushes=2 queue=0 l0files=2
[monitor] rotations=3 flushes=2 queue=0 l0files=2
[monitor] rotations=3 flushes=3 queue=0 l0files=3
[monitor] rotations=4 flushes=3 queue=0 l0files=3
write phase: 120000 records in 5.145s (23323 ops/s), max put latency 104.7ms
[monitor] rotations=4 flushes=4 queue=0 l0files=4
final state: rotations=4 flushes=4 l0files=4
verified 120000 records in 4.170s (memtable + L0 sstables)
engine closed: active memtable flushed, all WAL segments rotated out
post-reopen sample check passed (121 keys from L0 sstables)
data dir .../demo-data: L0-000000.sst(3.6MB) L0-000001.sst(3.6MB) L0-000002.sst(3.6MB) L0-000003.sst(3.6MB) L0-000004.sst(0.3MB)
```

说明: 监控行显示轮转与刷盘交替推进、队列始终被单线程刷盘协程及时清空;
写入全程无停顿(持续 2.3 万 ops/s), 仅轮转瞬间的 WAL fsync 带来单次毫秒级
长尾(每 4MB 一次, 是"安全轮转"的耐用性代价); 写完后 12 万条记录全量校验通过,
关闭后所有 WAL 段被清理, 重开引擎可直接从 L0 SSTable 读回数据。
