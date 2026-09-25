# kvdb — 追加写日志结构的 KV 存储（仅标准库）

单目录、追加写、墓碑删除、启动扫描建索引、段轮转、合并压缩、故障注入与崩溃恢复。
仅依赖 Go 标准库，测试全部通过 `go test` 运行。

## 设计

目录布局（目录由调用方指定）：

	MANIFEST          已提交的存活段集合（tmp 写入 + fsync + rename 原子替换 + 目录 fsync）
	000001.seg        已封存段（只读）
	000002.seg        活跃段（只追加）
	000007.compact    压缩暂存文件（提交前崩溃时由恢复流程清理）

- 记录格式：`| crc32(4B) | keyLen(4B) | valLen(4B) | flags(1B) | key | value |`，
  CRC 覆盖其后全部字节；`flags` 最低位为墓碑标记。
- 写路径：Put/Delete 都向活跃段追加一条记录（Delete 追加墓碑），
  然后更新内存索引 `key -> {段ID, 偏移, 长度, 是否墓碑}`。
- 启动恢复：读 MANIFEST 得到已提交段集合；磁盘上 ID 大于已提交最大值的段
  （轮转新段或压缩切换前崩溃留下的新段）也保留并幂等重放；其余 `.seg`、
  `*.compact`、`MANIFEST.tmp` 一律删除。逐段扫描重建索引，遇到 CRC 校验失败
  或半条记录即截断（撕裂尾部），截断后 fsync。
- 段轮转：活跃段超过 `MaxSegmentBytes` 后封存并开新段，新布局写入 MANIFEST。
- 压缩：把所有已封存段合并成一个新段，只拷贝当前可见记录——被覆盖的旧版本
  丢弃、墓碑整条消失（其历史全部在被压缩段内）。新段先写 `.compact` 暂存文件，
  fsync、rename 就位、目录 fsync，然后在临界区内更新索引并提交 MANIFEST，
  最后清扫旧段。
- 并发读固定段句柄：Get 在锁内查索引并对段 `refs++`，锁外用 `ReadAt` 读，
  结束后 `refs--`。压缩把旧段标记为 obsolete，但只有 `refs == 0` 才真正
  close + unlink，因此旧读者永远不会读到已回收文件。

## 崩溃恢复与断电保证的边界

保证（进程崩溃 / kill -9）：

- 已返回成功的写（`SyncWrites: true` 或之后调用过 `Sync()`）在重启后可见。
- 同键写-删-写，重放后只见末值；墓碑在压缩前屏蔽旧值，压缩后彻底消失。
- 压缩在 MANIFEST 提交前中断：暂存文件被清理，老布局原样可用；
  提交后中断：恢复按新清单加载，旧段作为孤儿删除。两个方向都可恢复。
- 段尾半条记录（撕裂写）在启动扫描时被 CRC 检出并截断。

断电额外要求：

- 断电语义依赖 fsync 点：记录 fsync（`SyncWrites` 或 `Sync()`）、
  新段创建后的目录 fsync、MANIFEST 的 tmp+fsync+rename+目录 fsync。
  在此之上，已确认的写与已提交的压缩在断电后均可恢复。

边界（不保证）：

- `SyncWrites: false` 且未调用 `Sync()` 时，最近一批已返回的写可能只停留在
  页缓存，断电即丢（进程崩溃不丢）。这是性能与持久性的显式权衡。
- rename 原子性依赖 POSIX 文件系统语义；某些网络/特殊文件系统不保证。
- CRC 能检测并截断腐坏点之后的所有数据，但不修复静默位翻转；
  段中间的腐坏会导致该点之后的记录被截断丢弃。
- 不提供多进程同时打开同一目录的互斥（需调用方自行加文件锁）。

## 故障注入点

通过 `Options.Injector` 在精确边界注入错误模拟崩溃/掉电：

| 注入点 | 位置 |
|---|---|
| `append.before-sync` | 记录已追加、fsync 之前 |
| `manifest.before-write` | 新 MANIFEST 写入之前 |
| `compact.before-commit` | 压缩新段已 rename 就位、索引/MANIFEST 切换之前 |
| `compact.after-commit` | MANIFEST 已提交、旧段清扫之前 |

## 运行

	go test ./...        # 全部测试（-race 亦通过）
	go run ./cmd/kvdemo  # 演示：写删写、轮转、压缩、模拟崩溃恢复

API：`Open(dir, *Options)`、`Put`、`Get`、`Delete`、`Sync`、`Compact`、`Close`。
