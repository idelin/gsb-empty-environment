# publisher — 基于日志的多文件原子发布器

一次事务更新任意多个虚拟文件；读者通过版本清单（manifest）永远只看到
完整的一代内容，不会看到逐文件混合的版本。

## 磁盘布局

```
journal            预写日志（begin / put* / commit，带 CRC 帧）
manifest.json      当前版本清单（由 manifest.tmp 原子 rename 安装）
manifest.tmp       清单暂存
data/<name>@<txn>  不可变的版本化数据块（blob）
```

## 发布协议（Publisher.Publish）

1. **记录意图**：向 journal 写入 `begin` + 每个文件一条 `put`（携带新内容），fsync。
2. **提交决议**：向 journal 追加 `commit` 记录并 fsync —— 这是唯一的原子提交点。
3. **应用**：把每个文件写到 `data/<name>@<txn>.tmp`，fsync 后 rename 就位，
   syncdir；再写 `manifest.tmp`、fsync、rename 覆盖 `manifest.json`，syncdir。
4. **检查点**：清空 journal 并 fsync；回收不再被引用的旧 blob（为在途读者
   多保留一代）。

## 恢复（Publisher.Recover）——只依据持久化日志

- 日志为空：无事发生。
- 有 `begin` 无 `commit`：**全回滚** —— 删除暂存的 blob 与 `manifest.tmp`，
  清空日志。旧清单始终是可见状态。
- 有 `commit`：**全完成** —— 用日志中记录的内容重做 apply（幂等），再检查点。

日志帧带长度 + CRC32，撕裂的尾部写在恢复时被忽略（见
`TestTornJournalTail`）。

## 读者一致性

读者读 `manifest.json` 得到一份 `Snapshot`：清单由原子 rename 安装，因此
一份快照永远引用完整的一代；快照内的 blob 不可变且带 SHA-256 校验
（`Snapshot.Get` 读取时验证）。

## 故障注入与切点枚举

`FaultFS` 包装 `MemFS`，把每个变更型文件操作（create / write / sync /
rename / remove / syncdir）编号为一个**切点**：

- `FailAt(n, err)`：第 n 个操作返回注入错误（不执行）。
- `CrashAt(n)`：第 n 个操作执行后整机崩溃 —— `MemFS.Crash` 把易失命名
  空间回滚到持久化状态（文件内容以 `File.Sync` 为准，目录项以
  `SyncDir` 为准），之后所有操作返回 `ErrCrashed`，直到 `Restart()`。

一次三文件发布共 31 个切点。`TestCrashEnumeration` 在每个切点崩溃后
恢复，断言：可见状态要么是完整的旧一代、要么是完整的新一代，恢复幂等，
且之后可继续发布。`TestFailureEnumeration` 对每个切点注入 I/O 错误做同
样验证。`TestReaderSeesConsistentGeneration` 在发布期间的每个操作之后
取快照，验证读者永远看到完整一代。

## 运行

```sh
go test -race ./...
```

生产环境使用 `OSFS`（真实文件系统），测试使用 `MemFS` + `FaultFS`。
