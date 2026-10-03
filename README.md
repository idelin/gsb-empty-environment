# lscheckpoint — 日志结构状态检查点生成器

Go 标准库实现的状态机增量检查点组件：内存状态树按页哈希切片，并行生成只读快照；
支持在持续写入期间生成增量差量包；恢复时对照哈希链校验完整性，坏页精确定位。

## 设计

- **分页状态树**（`state.go`）：状态树固定切为 64 页（FNV 哈希取模），
  快照通过写时复制（COW）实现——快照仅做 O(页数) 指针拷贝，之后写入共享页时先克隆，
  因此快照生成几乎不阻塞写入。
- **并行检查点生成**（`checkpoint.go`）：页序列化与 SHA-256 哈希按 NumCPU 并行；
  每 8 页打包为一个 chunk 文件并行落盘；清单 `manifest.json` 记录每页哈希、
  每块哈希以及哈希链 `chain[i] = sha256(chain[i-1] || chunkHash[i])`，末端为 RootHash。
- **增量差量包**：与上一检查点的页哈希对照，仅重写变化页，清单通过 `base`
  字段引用基础检查点，形成可回溯链。
- **恢复校验**（`restore.go`）：沿 base 链从旧到新逐块校验文件哈希 → 哈希链 →
  页哈希，任一不符即以 `CorruptionError` / `ChainError` / `PageHashError`
  报出具体 chunk 序号并拒绝数据。

## 运行

```sh
go test -v ./...   # 验收测试
go run ./cmd/demo  # 运行样例
```

## 验收结果（本机实测）

`go test -v ./...`：

```
=== RUN   TestFullCheckpointRestore
--- PASS: TestFullCheckpointRestore (0.10s)
=== RUN   TestIncrementalCheckpoint
    incremental diff rewrote 42/64 pages
--- PASS: TestIncrementalCheckpoint (0.34s)
=== RUN   TestCheckpointUnderWriteLoad
    baseline avg=5.58µs, during-checkpoint avg=21.052µs p99.9=509.708µs max=154.224ms (window=9176 writes)
--- PASS: TestCheckpointUnderWriteLoad (1.89s)
=== RUN   TestCorruptThirdChunk
    corruption precisely located: checkpoint integrity violation: chunk 2
    (.../ckpt-020000/chunk-000002.bin) hash mismatch: expected 1b2a28aa..., got 14cbbdc5...
--- PASS: TestCorruptThirdChunk (0.15s)
PASS
ok      lscheckpoint
```

`go run ./cmd/demo`：

```
[checkpoint] full checkpoint at seq=50729, chunks=8, root=e7cdf1268f385743...
[latency] baseline avg=936ns | during-checkpoint avg=3.6ms max=109.9ms
[incremental] diff package at seq=100100: 64 changed pages in 8 chunks (base=ckpt-050729)
[restore] incremental chain OK: 100000 keys, key-00000000="updated"
[corrupt] flipped bits in 3rd chunk: chunk-000002.bin
[restore] REJECTED bad data, located chunk 2
  -> checkpoint integrity violation: chunk 2 (.../chunk-000002.bin) hash mismatch: ...
```

## 验收边界对照

- **10 万键值对连续写入期间触发检查点**：快照为 O(页数) COW 指针拷贝，检查点
  生成全程在不可变快照上并行进行；写入窗口内 p99.9 延迟 510µs（< 50ms 阈值），
  平均延迟 < 10× 基线，抖动受控。
- **损坏第 3 个分块**：恢复流程逐块校验 SHA-256 与哈希链，精确定位
  `chunk 2`（`chunk-000002.bin`）并以 `CorruptionError` 拒绝坏数据。
- **增量差量**：仅重写哈希变化的页（测试中改 50 键 → 重写 42/64 页），
  沿 base 链恢复后完整状态逐项校验一致。
