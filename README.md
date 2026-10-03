# aries — ARIES 崩溃恢复引擎 + 稀疏数据内存紧凑优化器（Go，仅标准库）

实现数据库经典 ARIES 崩溃恢复协议的三阶段扫描，以及面向海量稀疏数据的
自适应压缩页容器。

## 结构

| 文件 | 内容 |
|---|---|
| `wal.go` | 预写日志：UPDATE/COMMIT/ABORT/END/CLR/CHECKPOINT 记录，varint 编码，追加即 fsync，并发安全 |
| `sparse.go` | `SparsePage`：稀疏 map ↔ 致密数组自适应转换（25%/12.5% 迟滞阈值），RLE+varint 压缩序列化 |
| `storage.go` | `PageStore`：追加式压缩页文件，打开时扫描重建索引 |
| `db.go` | 事务运行时：Begin/Update/Commit/Abort/Checkpoint/Flush/Crash |
| `recovery.go` | ARIES 三阶段：Analysis（重建 DPT/TT）→ Redo（历史重复，pageLSN 门控）→ Undo（反向扫描 + CLR） |
| `scenario.go` | 验收场景：3 个未提交事务 + 2 个已提交事务，Checkpoint 后崩溃 |
| `cmd/demo` | 可运行样例，打印 WAL、三阶段过程与最终校验 |

## 运行样例

```sh
go run ./cmd/demo
```

输出 WAL 内容、Analysis 得到的 DPT/losers、Redo/Undo 计数，并逐单元格
校验：已提交数据持久化、未提交数据回滚（全部 PASS）。

## 测试

```sh
go test -race -count=1 -v ./...
```

覆盖：

- `TestCrashRecoveryThreePhases` — 验收边界：checkpoint 后崩溃，三阶段扫描，
  回滚 3 个未提交事务（含 1 个 aborting），持久化 2 个已提交事务
- `TestRecoveryIdempotent` — 恢复可重入（undo 中途再崩溃，二次恢复零 redo/undo）
- `TestRecoveryWithoutCheckpoint` — 无检查点全日志恢复
- `TestWALConcurrentAppend` — 16 goroutine 并发追加，LSN 唯一且单调
- `TestAdaptiveConversion` / `TestCompressionEffectiveness` — 容器自适应转换
  边界（恰好阈值 ±1）、RLE 压缩率
- `TestSparsePageConcurrent` — 8 goroutine × 5000 次混合读写（`-race`）
- `TestNoMemoryLeak` — 清空后容器必须释放致密数组；GC 后 HeapAlloc 不增长、
  goroutine 数不泄漏

最近一次运行：12 个测试全部 PASS（`-race`，约 25s）。
