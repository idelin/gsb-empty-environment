# publisher

一个 Go 版多文件原子发布器：一次 `Publish` 更新多个虚拟文件，读者通过
版本清单（manifest）始终看到一致集合，崩溃恢复完全依据持久化日志决定
整体回滚或整体完成。

## 磁盘布局

```
<dir>/
  journal            # 预写日志（JSON 行：begin / commit / done）
  manifest           # 当前版本清单：{seq, files: {name: version}}
  manifest.tmp       # 清单暂存，rename 原子替换 manifest
  files/<name>.<seq> # 不可变的版本化文件内容
```

## 发布协议（Publish）

1. 向 journal 追加 `begin` 记录并 fsync —— 记录**新内容**与发布后的完整清单；
2. 写入每个 `files/<name>.<seq>`，逐文件 fsync，再 fsync `files/` 目录；
3. 向 journal 追加 `commit` 记录并 fsync —— 这是**提交决议**；
4. 写 `manifest.tmp` + fsync，`rename` 覆盖 `manifest`，fsync 根目录；
5. 向 journal 追加 `done` 记录并 fsync，随后回收未被钉住的旧版本。

## 恢复规则（只看持久化日志）

- 日志尾部无未完成事务：什么都不做；
- 尾部是 `begin` 而无 `commit`：**全回滚** —— 删除该事务已写的版本文件与
  暂存清单，追加 `done` 关闭事务；
- 尾部是 `begin` + `commit` 而无 `done`：**全完成** —— 仅凭 `begin` 记录中
  的内容幂等重写版本文件、重装清单，追加 `done`。

恢复本身再崩溃也安全：所有重做步骤幂等，下次启动重复执行即可。

## 读者一致性

`Snapshot()` 钉住当前清单（引用计数），`Read(name)` 按清单解析到不可变的
`files/<name>.<ver>`。清单通过 `rename` 原子切换，因此读者永远不会逐文件
看到混合版本；并发发布期间旧快照仍读到旧的一致集合，GC 只回收无任何
快照引用的版本。

## 故障注入与切点枚举

`FaultFS`（faultfs.go）是内存版 `FS`，按真实崩溃语义建模：

- `File.Sync` 之前写入的内容不持久；
- `SyncDir` 之前 create/rename/remove 的命名空间变更不持久；
- `Crash` 丢弃一切非持久状态，`Reboot` 后仅持久状态可见。

每个变更操作（`create/write/sync/rename/remove/syncdir`）都会记录到操作
日志。`Arm(n)` 在第 n 个操作前注入崩溃。一次三文件发布共枚举出 27 个
切点（日志写/刷盘 ×3、版本文件写/刷盘 ×3、目录刷盘、清单写/刷盘/
rename、GC 删除等）。`TestCrashAtEveryCutPoint` 在每个切点处崩溃并验证：
恢复后读者要么看到完整的旧集合，要么看到完整的新集合，绝不混合。

## 运行测试

```sh
go test ./...
go test -run TestCrashAtEveryCutPoint -v .   # 打印全部切点
```
