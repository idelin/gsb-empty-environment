# storetool — manifest / 段文件 / 索引三方一致性审计与可恢复修复

纯 Go 标准库实现。对调用方指定目录中的存储做**只读**一致性审计，产出可执行的
修复计划（JSON）；`apply` 在执行前校验原状态摘要，并通过日志（journal）支持
中断恢复。故障通过环境变量注入点模拟。

## 目录布局（全部由调用方 `-dir` 指定）

```
<dir>/
  manifest.json        # 段清单：每个段的 name/size/sha256，以及索引的 name/size/sha256
  index.json           # key -> {segment, offset, length}
  segments/seg-*.dat   # 追加式记录：u32 keyLen | u32 valLen | key | value | u32 crc32
  repair-journal.json  # apply 运行期间存在，完成后删除
```

修复计划文件由 `audit -plan-out` 写到调用方指定路径；**该路径必须位于存储目录
之外**（audit 强制检查），以保证审计本身不改动被审计目录。

## 命令

```
storetool gen    -dir D [-segments N] [-keys K]   # 生成演示存储
storetool audit  -dir D [-plan-out plan.json]     # 只读审计；退出码 0=一致 1=有问题 2=错误
storetool apply  -dir D -plan plan.json           # 执行计划；退出码 0=完成 4=含不可恢复项 2=错误
```

## 审计结论与修复动作

| 情形 | issue | 动作 |
|---|---|---|
| 索引缺失/损坏/过期，且所有段完好 | `index-missing` / `index-corrupt` / `index-hash-mismatch` / `index-stale` / `index-entry-invalid` | `rebuild-index` + `sync-manifest` |
| 段校验失败或丢失 | `segment-checksum-mismatch` / `segment-size-mismatch` / `segment-missing` | `unrecoverable`（仅报告，**不编造内容**） |
| 段损坏同时索引也坏 | 追加 `index-rebuild-withheld` | 不重建索引（避免把数据损失"固化"进新索引） |
| 磁盘上存在 manifest 未登记的段 | `segment-orphan` | `remove-orphan` |
| manifest 不可读 | `manifest-unreadable` | `unrecoverable`（不伪造 manifest） |

索引重建是从段文件顺序扫描得来的确定性结果：只要段完好，索引永远可以重建；
段内容一旦校验失败，原始字节无法从其它两处推导，因此只报告、不修复。

## 状态摘要与中断恢复协议

- `StateSummary(dir)` 把 manifest、index（含"缺失"这一状态）、`segments/` 下全部
  文件的内容哈希折叠成一个 SHA-256 摘要，审计时写入计划的 `state_summary`。
- `apply` 启动时：
  - **无日志**（全新执行）：重算当前摘要，与计划不符则拒绝执行（审计后状态已变，
    需重新审计）；随后先原子写入一份空日志再开始执行。
  - **有日志**：日志中的计划摘要须与计划一致（否则拒绝，防止串计划）；信任日志，
    跳过已完成步骤，幂等重做剩余步骤。
- 每个步骤幂等：`rebuild-index`/`sync-manifest` 走 tmp+rename，重做无副作用；
  `remove-orphan` 对已删除文件视为成功。全部完成后删除日志。
- 计划是单次性的：成功执行后状态摘要已改变，同一计划再次 apply 会因摘要不符
  被拒绝——这是预期行为，应重新审计。

## 故障注入点

`STORETOOL_FAILPOINT="<actionID>:<phase>"`，命中即崩溃（`os.Exit(2)`，测试中替换
为 panic）。phase：`before`（步骤前）、`pre-rename`（tmp 已落盘、rename 前）、
`post-rename`（rename 后、写日志前）、`pre-journal`（写日志前）。

## 进程崩溃恢复与断电保证的边界

**保证（POSIX 文件系统、fsync 可信的前提下）：**

- 所有状态变更都是"tmp 文件 + fsync + rename + 目录 fsync"。rename 是原子的：
  任何时刻观察者只能看到旧版本或新版本，不会看到半个文件。
- 进程在任意注入点崩溃后，重新执行同一 `apply` 即可恢复：日志存在则断点续作，
  日志不存在则重新校验摘要。步骤幂等保证"重做"安全。
- 崩溃后可能残留 `*.tmp` 文件，不影响正确性（不计入状态摘要，下次写会覆盖）。
- 审计路径只以只读方式打开文件，任何时刻中断都不会改动被审计目录。

**不保证（明确边界）：**

- 断电/内核崩溃与进程崩溃的唯一区别是页缓存丢失；上述 fsync 序列正是为此设计。
  但若硬件/文件系统对 fsync 撒谎（漏报写回、禁用写屏障的挂载选项、某些网络/虚
  拟化存储），原子性与持久性承诺随之失效——这属于存储栈契约之外。
- 非 POSIX 语义文件系统（如某些网络盘 rename 不原子）不在保证范围内。
- 段文件本身的校验失败**不可恢复**：工具不编造内容，需从备份/上游恢复。
- 日志存在后 apply 信任日志；同时篡改存储与日志属于主动破坏，不在防护范围。
- 崩溃恢复以"重新执行 apply"为前提；工具不做后台自动恢复。

## 测试

```
go test ./...
```

覆盖：记录编解码与截断/位腐检测、状态摘要确定性与敏感性、干净库审计、审计只读
性（前后快照逐字节比对）、缺索引重建计划、段损坏不可恢复且不重建索引、孤儿段、
过期索引、plan-out 拒绝写入存储目录、索引重建往返一致、摘要变化拒绝执行、
post-rename / pre-rename 崩溃后恢复、apply 不改动损坏段、孤儿删除、异计划日志拒绝。
