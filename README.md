# lockfreemvcc — 只读事务非阻塞无锁读取器

面向高频读业务的无锁 MVCC 读路径（Go 标准库实现，零依赖）。

## 设计

- **不可变版本链**：每个键一条单向版本链，节点创建后绝不原地修改；
  链头为 `atomic.Pointer`，读路径纯原子加载遍历，不获取任何锁。
- **快照即时间戳**：只读事务 = 一次全局提交计数器（`atomic.Uint64`）的
  原子读。遍历版本链时取首个 `commitTS <= snapshotTS` 的版本，
  与并发写入完全隔离，绝不阻塞。
- **写事务原子发布**：写线程先把本次提交的所有版本节点链接进各自
  版本链，最后才原子发布提交时间戳。Go 原子操作顺序一致，
  因此快照覆盖该时间戳的读者必然能看到本次提交的全部节点 ——
  多键提交要么全见、要么全不见，**零撕裂、绝无半脏数据**。
  多个写线程之间用互斥锁串行化（写可等写，读永远不等）。
- **无阻塞版本回收（GC）**：读者把快照时间戳发布到无锁槽位注册表；
  写线程定期计算 `gcHorizon = min(活跃快照, 当前提交点)`，
  采用"先发布水位、再复核注册表"的两阶段协议，只截断任何活跃或
  正在注册的读者都不可见的旧版本。注册与回收竞态由 `Begin` 中的
  水位校验 + 重试关闭。

## 文件

- `mvcc.go` — 核心实现（Store / Snapshot / Commit / Transfer / GC）
- `mvcc_test.go` — 单元测试 + 验收边界测试
- `cmd/demo/main.go` — 可运行的验收场景演示

## 运行

```sh
go test -v ./...        # 全部测试（含验收边界）
go test -race ./...     # 竞态检测
go run ./cmd/demo       # 运行样例：1 写线程 + 5 读线程，3 秒压测
```

## 验收边界

`TestConservationUnderContention`：1 个写线程对 1000 个键持续密集执行
守恒转账（`Transfer`，单事务两键原子提交）；5 个读线程持续执行全表
扫描校验和统计。断言每次快照的总和恒等于初始总和（零撕裂），且
写、读双方都持续推进（无等待）。

## 测试结果（Apple Silicon, Go 1.24.2）

```
$ go test -race -v ./...
=== RUN   TestSnapshotIsolation
--- PASS: TestSnapshotIsolation (0.00s)
=== RUN   TestMultiKeyCommitAtomicity
--- PASS: TestMultiKeyCommitAtomicity (0.00s)
=== RUN   TestGCReclaimsAndKeepsVisibleVersions
--- PASS: TestGCReclaimsAndKeepsVisibleVersions (0.01s)
=== RUN   TestReadersNeverBlockOnWriter
    mvcc_test.go:158: completed 187339 consistent scans during concurrent writes
--- PASS: TestReadersNeverBlockOnWriter (0.51s)
=== RUN   TestConservationUnderContention
    mvcc_test.go:238: OK: 133316 commits, 10933 full-table scans in 2s, 0 torn reads (sum conserved at 1000000)
--- PASS: TestConservationUnderContention (2.00s)
PASS
ok      lockfreemvcc    3.922s
```

运行样例（`go run ./cmd/demo`，无 race 检测）：

```
duration        : 3s
keys            : 1000 (initial total 1000000)
writer commits  : 1701460 (567153 commits/s, never blocked by readers)
reader scans    : 660562 (220187 scans/s across 5 readers, lock-free)
torn snapshots  : 0
RESULT: PASS - every snapshot observed the conserved sum, zero torn reads
```
