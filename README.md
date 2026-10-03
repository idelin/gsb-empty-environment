# roaring — 压缩位图(Go,仅标准库)

紧凑存储的 uint32 位图,支持自动容器选择与高效布尔集合运算,内置
异常注入与崩溃恢复容错机制。

## 容器自动选择

每个 64k 分片(高 16 位)独立选择最优编码:

| 容器 | 适用场景 | 存储 |
|---|---|---|
| `arrayContainer` | 稀疏离散 ID(≤4096 个值) | 2 B/值 |
| `bitmapContainer` | 密集随机分布 | 固定 8 KiB |
| `runContainer` (RLE) | 密集连续区间 | 4 B/区间 |

插入、集合运算后自动重新评估并转换(见 `container.go`、`ops.go`)。

## 集合运算

`And` / `Or` / `Xor`:bitmap-bitmap 走 1024 字位运算,run-run 走区间
归并(不物化区间内的值),其余组合走"小容器迭代 + 大容器探测"。

## 容错设计

- **WAL 先行**:每次变更先写日志并 fsync,再应用内存状态;写失败
  (如磁盘满)整体回滚,内存与磁盘保持一致(`store.go`)。
- **崩溃恢复**:快照 = magic + CRC32 + 序列化位图,经 tmp 文件原子
  rename;WAL 记录带长度 + CRC32 帧,恢复时重放有效前缀,撕裂/损坏
  的尾部记录就地截断(`wal.go`、`serialize.go`)。
- **异常注入**:`SimFS` 模拟页缓存,`Crash()` 丢弃未 fsync 数据
  (断电),`DiskFull` 注入 ENOSPC,`SimulateTornWrite` 注入撕裂写,
  `CorruptCommitted` 注入静默磁盘损坏(`vfs.go`)。
- **网络丢包自愈**:停等 ARQ 复制协议,CRC + 序列号 + ACK + 超时
  重传,重复包去重(`replicate.go`)。

## 运行

```sh
go test ./...          # 单元 + 异常注入 + 崩溃恢复 + 并发测试
go test -race ./...    # 竞态检测
go test -run '^$' -bench . ./...
go run ./cmd/demo      # 运行样例
```
