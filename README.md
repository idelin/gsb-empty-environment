# pagebackup — 页版本全量/增量备份链与可校验恢复

纯 Go 标准库实现（兼容 Go 1.11+）。数据库建模为固定大小（4096 字节）的页集合，
每次 `ApplyChanges` 产生一个新版本；备份按版本导出，恢复按基线摘要和序号逐环校验。

## 目录结构

```
db.go         页版本数据库（pages/%08x.pg + state.json），内容摘要
manifest.go   备份清单：类型/base/target/每页 sha256/整库摘要/自校验和
export.go     ExportFull / ExportIncremental（staging + rename 原子出现）
restore.go    Restore（链式校验 + staging + rename 原子提交）
fault.go      故障注入点（FaultInject 模拟 I/O 错误，CrashInject 模拟崩溃/断电）
util.go       writeFileAtomic / fsyncDir
backup_test.go  全部测试（go test）
cmd/demo/     端到端运行样例
```

## 校验规则

- 清单自校验和（sha256）防篡改、防截断；清单内每页 payload 的 sha256 逐一核对。
- 序号链：全量备份建立基线版本；增量 `base == 当前版本` 才可接续；
  `target == 当前版本` 视为重复应用，幂等跳过；其余（跳环、乱序、缺全量）一律报错。
  因此增量 2（base=1）不能直接接在全量 0（v0）之后跳过增量 1。
- 每应用一环，重建库的整库摘要必须等于该环清单记录的基线摘要，否则中止。
- 任一环节失败：staging 目录被清除，目标路径不产生"表面可用"的部分数据库。

## 崩溃恢复与断电保证边界

**保证内（依赖文件系统语义）：**

- 导出与恢复都先写入 `<目标>.staging.<随机>` 目录，所有页文件
  `write + fsync + rename` 落盘，随后 fsync staging 目录，最后
  `rename(staging, 目标)` 并 fsync 父目录。rename 是原子点：
  崩溃/断电发生在 rename 之前，目标路径要么不存在、要么是上一个完整版本；
  发生在 rename 之后，目标即为完整且摘要已校验的版本。
- 崩溃遗留的 staging 目录不是合法数据库；下一次 `Restore` 启动时按
  `<目标>.staging.*` 回收。`Restore` 返回 nil 之后，结果对已提交的
  rename 持久（假设硬件不虚报 fsync）。

**保证外（明确边界）：**

- staging 与目标必须位于同一文件系统（rename 跨设备非原子，本实现直接报错失败）。
- 不防御虚报 fsync 的硬件/文件系统、整机掉电时的盘片缓存丢失。
- 源数据库的 `ApplyChanges` 本身不是崩溃原子的（页已写、state 未写会留下
  超前页）；持久性保证针对备份与恢复路径，源库仅作演示。
- 仅支持页的新增/覆盖，不支持页删除（tombstone 留作扩展）。

## 运行样例

```
$ go run ./cmd/demo /tmp/pagebackup-demo
[export] full-v1     -> /tmp/pagebackup-demo/backups/full-v1
[export] incr-v1-v2  -> /tmp/pagebackup-demo/backups/incr-v1-v2
[export] incr-v2-v3  -> /tmp/pagebackup-demo/backups/incr-v2-v3

[restore] full-v1 + incr-v1-v2 + incr-v2-v3 -> version=3
[verify]  source   digest 9e7594f74cd293a6e4fa50140a814a8d06accf4cbc47b5cd67c049fba36d6eb9
[verify]  restored digest 9e7594f74cd293a6e4fa50140a814a8d06accf4cbc47b5cd67c049fba36d6eb9 (equal=true)

[ok]      re-applying incr-v2-v3 is idempotent (no-op)

[reject]  full-v1 + incr-v2-v3 (skips v2): backup 1: broken chain: incr base=2 target=3 cannot apply on top of version 1 (missing or out-of-order link)
[reject]  partial db at target: false

[reject]  corrupted page in incr-v2-v3: backup 2: page 3 corrupt: manifest hash 0c791370..., got 5ed7fcbc...
[reject]  partial db at target: false

all demo artifacts under caller-specified dir: /tmp/pagebackup-demo
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestFullRestoreDigestEqualsSource            --- PASS
=== RUN   TestIncrementalChainRestoreDigestEqualsSource --- PASS
=== RUN   TestGapRejectedAndNoPartialDB                --- PASS  (增量2跳过1接全量0 → 拒绝，无残留)
=== RUN   TestMissingLinkRejected                      --- PASS  (缺全量 → 拒绝)
=== RUN   TestIdempotentReapply                        --- PASS  (重复应用同增量幂等)
=== RUN   TestCorruptedPageRejected                    --- PASS  (页损坏 → 拒绝，无部分库)
=== RUN   TestCorruptedManifestRejected                --- PASS  (清单篡改 → 校验和拒绝)
=== RUN   TestFaultInjectedDuringExport                --- PASS  (注入I/O故障 → 导出中止且清理)
=== RUN   TestCrashDuringExportYieldsUnusableBackup    --- PASS  (导出中崩溃 → 备份不可用而非半成品)
=== RUN   TestCrashDuringRestoreLeavesNoPartialAndRecovers --- PASS (恢复中崩溃 → 无部分库，重跑自愈)
=== RUN   TestFilesStayInCallerSpecifiedDir            --- PASS  (文件全部落在调用方指定目录)
PASS
ok      pagebackup    2.208s
```
