# segstore — 新段构建 / 校验 / 发布 / 回收状态机

纯 Go 标准库实现。调用方指定一个目录，所有状态都在该目录内：

| 文件 | 角色 |
| --- | --- |
| `journal.json` | 进行中事务的 WAL，记录状态机当前阶段、新段名、大小、SHA-256、旧段列表 |
| `manifest.json` | 已发布的权威指针，带整体 SHA-256 校验和，指向当前段 |
| `seg-NNNNNN.dat` | 段数据文件，内容即提交的 payload |
| `*.tmp` | 原子写的临时文件，崩溃后属于垃圾 |

## 状态机

```
building -> validating -> publishing -> reclaiming -> (删除 journal) -> idle
```

- **building**：先写 journal，再把段数据写到 `seg-N.tmp`，`fsync` 后原子 rename 为 `seg-N.dat`。
- **validating**：对盘上段重新计算大小 + SHA-256，与 journal 不符即报 `ErrCorrupt`，绝不发布。
- **publishing**：生成新 manifest（含校验和），同样 `tmp + fsync + rename + fsync(dir)` 原子替换。
  rename 原子性保证崩溃后只能看到**旧的或新的**、各自自洽的 manifest，不会出现半截 manifest。
- **reclaiming**：删除上一版 manifest 引用、但新 manifest 不再引用的旧段；幂等，重复删除无害。
- 每个阶段开始前先把新阶段写入 journal，因此崩溃后 `Open` 知道从哪里续跑。

## 任一步中断恢复与幂等重试

`Open(dir, fault)` 会自动执行 `recover()`：

- journal 在 `validating/publishing/reclaiming`：新段已完整落盘（校验和已记录），
  直接**前滚**，每一步都幂等（段已存在且匹配则跳过写；manifest 重写等价；旧段删除可重复）。
- journal 在 `building`：
  - 盘上段已完整且校验和匹配 → 前滚到 validating；
  - 段缺失/半截 → 该数据**尚未确认**，事务中止：删除半截段（孤儿）、清 journal，
    保留旧 manifest 不变，调用方用原 payload 重试 `Commit` 即可（txn 号复用，不产生重复段）。
- 无 journal：直接校验并加载 manifest。

调用方的重试规则很简单：`Commit` 返回错误后，丢弃 `Store`，重新 `Open` 再 `Commit`。
`Commit` 开始时会先前滚任何未完成事务。

## 引用关系与孤儿回收

- **发布前，新段不被 manifest 引用，属于孤儿**；但进行中的 journal 引用它，因此
  `CollectOrphans()` 在事务存活期间保护它（崩溃恢复还需要它）。
- 中止的 building 事务的半截段、以及任何 `seg-*.dat` 未被 manifest **且** 未被
  in-flight journal 引用的文件，都会被 `CollectOrphans()` 删除；`*.tmp` 同理。
- **发布后，旧段不再被 manifest 引用，可以回收**：状态机 reclaiming 阶段自动删除，
  也可随时调用 `CollectOrphans()` 按引用兜底。

## 损坏检测：绝不静默回退

- manifest 引用的段缺失 / 大小不符 → `ErrCorrupt`；
- manifest 自身校验和不匹配（撕页、篡改）→ `ErrCorrupt`；
- manifest 丢失但目录里有段文件 → `ErrCorrupt`（不把孤儿当当前数据）。

出现 `ErrCorrupt` 时 store 拒绝启动，不会回退到上一版 manifest——旧 manifest 指向的是
更旧的数据，静默回退等于丢掉已经确认（published & acknowledged）的数据。
`Verify()` 还会对所有引用段重算 SHA-256，用于检测位腐烂。

## 故障注入

`Open(dir, FaultFunc)` 的回调在 8 个注入点触发：

`before_build` `after_build` `before_validate` `after_validate`
`before_publish` `after_publish` `before_reclaim` `after_reclaim`

回调返回 `ErrInjectedFault` 即模拟进程崩溃：立即返回、不动当前 journal，
测试里丢弃 Store 再 `Open` 同一目录验证恢复。生产代码传 `nil`。

## 进程崩溃恢复与断电保证的边界

- **进程崩溃（kill -9 / panic / 机器重启后 OS 完好）**：所有关键写入都走
  “临时文件 + `fsync` 数据 + 原子 rename + `fsync` 目录”。journal 与 manifest
  的更新要么完整可见、要么不可见，`Open` 可无损前滚。`Commit` 返回 nil 之后崩溃，
  新 manifest 与新段一定还在；`Commit` 返回之前崩溃，数据可能丢失也可能前滚，
  但调用方本就没有收到确认，语义安全。
- **断电**：本库能保证的边界依赖操作系统与硬件兑现 `fsync`/`FUA`：
  - 在正常兑现 flush 的本地 POSIX 文件系统上，`Commit` 返回 nil 后断电，已确认数据不丢；
    rename 后对目录的 `fsync` 保证目录项变更落盘，否则极端情况下断电可能丢 rename。
  - 若磁盘写缓存谎报 flush（部分消费级硬件/外置盘）、或底层网络/联合文件系统不保证
    rename 原子性与目录 fsync，任何应用层软件都无法提供严格持久化保证，本库也不例外。
  - 未覆盖多进程/多机并发写同一目录；该场景需要额外的文件锁。

## 运行

```bash
go test ./... -v
go run ./cmd/demo -dir ./demo-data -fault after_publish
```

`-fault` 可取上述任一注入点；`before_build` 演示“中止未确认事务 + 调用方重试”路径，
其余点演示“自动前滚”路径。
