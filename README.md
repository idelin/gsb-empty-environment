# storetool — manifest / 段文件 / 索引三方一致性审计与修复

只读审计存储目录中的三方一致性（`manifest.json`、`seg-*.dat`、`index.json`），
产出可执行的修复计划；应用计划前重新验证原状态摘要，支持中断恢复。
仅使用 Go 标准库。

## 布局

```
store/format.go   段记录格式(CRC32+长度前缀)、manifest/index 结构、原子写
store/audit.go    只读审计:逐段校验 size+sha256、索引逐条回查段内记录
store/plan.go     修复计划与状态摘要(sha256)
store/apply.go    应用器:摘要验证、预写日志(WAL)、幂等步骤、断点续做
store/fault.go    故障注入点(测试与 CLI 演示用)
store/gen.go      确定性样例仓库生成器
cmd/storetool     CLI: gen / audit / apply
```

## 用法

```sh
storetool gen   -dir D                # 生成确定性样例仓库
storetool audit -dir D -out plan.json # 只读审计,输出修复计划(不改动任何文件)
storetool apply -dir D -plan plan.json# 验证摘要后执行,可中断恢复
STORETOOL_FAULT=after_rename storetool apply ...  # 注入点模拟崩溃
```

## 修复策略

| 审计结果 | 计划 |
|---|---|
| 三方一致 | `noop` |
| 索引缺失/损坏/不一致,但段全部通过校验 | `rebuild_index`(索引是派生数据,从段扫描重建) |
| 段缺失或 sha256 不匹配 | `unrecoverable`,拒绝执行——**绝不编造段内容** |
| manifest 缺失/不可解析 | `unrecoverable`(期望摘要未知,无法安全验证) |
| 磁盘上存在 manifest 未登记的段文件 | 仅报告(orphan),不删除 |

## 一致性与恢复保证

**审计只读**:只以只读方式打开文件;测试用前后全目录哈希快照证明零改动。

**应用前验证**:摘要绑定 manifest 字节、每段校验结果、索引字节以及目录内
全部常规文件的清单(名称+大小+sha256)。首次运行时重算摘要不匹配即中止,
不写任何文件;恢复时校验 journal 的 plan ID,拒绝外来计划。

**进程崩溃(kill/ panic /注入故障)**:每个步骤先写临时文件 + fsync,再原子
rename,最后 fsync 目录;journal 在首步之前预写(WAL),每步完成后更新。
崩溃后重跑 `apply`:已完成的步骤按 journal 跳过,崩溃点所在的步骤是幂等的
(重建索引产出确定性字节),直接重做。崩溃窗口"rename 已生效、journal 未更新"
由预写日志覆盖:恢复时走 journal 路径而非重新校验初始摘要。

**断电保证的边界**:
- 保证成立的前提:文件系统提供原子 rename(POSIX 语义)、fsync 真正落盘。
- 任一时刻断电,恢复后要么看到旧状态、要么看到新状态,不会出现半写文件;
  未 journal 的步骤会重做,已 journal 的步骤不会重做。
- **不保证**:存储设备虚报 flush(硬件掉电缓存)、文件系统损坏、manifest 本身
  丢失且段已损坏(此时摘要无从验证,只能人工介入)、断电恰好发生在
  `O_APPEND` 之外的非原子元数据操作上表现依赖具体文件系统实现。
- 多步计划的整体性:保证的是"每步原子 + 整体可续做",不保证多步在一个
  事务里同生共死——崩溃后中间态(部分步骤完成)是合法且可恢复的。

## 测试

```sh
go test ./store/ -v
```

覆盖:一致仓库 noop 且审计只读、缺索引重建(字节级一致)、段损坏拒绝修复且
内容不被改动、审计后状态变更导致 apply 中止、四个注入点崩溃后恢复收敛、
外来计划被拒绝、索引条目被篡改的检测与修复、孤儿段只报告不删除、
manifest 缺失不可恢复。
