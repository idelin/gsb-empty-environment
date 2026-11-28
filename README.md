快照隔离（SI）虽然避免了脏读与不可重复读，却会放行写偏斜（Write Skew）与只读异常（Read-Only Transaction Anomaly）；PostgreSQL 的可串行化快照隔离（**SSI**）通过在 SI 之上追踪 **SIREAD 谓词锁** 与 **`rw-antidependency`（读-写反依赖）危险结构**，以极低误杀率实现真正的可串行化，四处机制极其微妙：1）**SIREAD 谓词锁与页/表粒度升级**：每个可串行化事务的读操作（点查与范围扫描！）都在内存登记非阻塞的 `SIREAD` 锁（绝不阻塞并发读写！），并维护索引叶页/范围锁覆盖新插入键以捕获幻读反依赖；当单事务锁数超过阈值时自动把元组级 `SIREAD` 升级为页级乃至表级锁（保留原有冲突检测的保守完备性！）；2）**双方向 `rw-antidependency` 边建立**：每当发生“事务 `T1` 读不到事务 `T2` 的写版本（因为 `T2` 在 `T1` 快照之后提交或尚在运行）”时，建立有向边 `T1 --rw--> T2`（两种触发时机：一是 `T2` 写某键时命中 `T1` 已持有的 `SIREAD` 锁；二是 `T1` 读某键时发现已有更晚版本的 `T2` 写入对 `T1` 快照不可见！）；3）**危险结构（Dangerous Structure）判定与延迟清理**：只要存在任意**枢纽事务（Pivot）`T2`**同时拥有一条入边 `T1 --rw--> T2` 和一条出边 `T2 --rw--> T3`（其中 `T1` 与 `T3` 可为同一事务形成 2-环 Write Skew，或为不同事务形成 3-事务只读异常，且 `T3` 已先于 `T1`/`T2` 提交！），即构成必须打断的危险结构！特别注意：**已提交事务的 `SIREAD` 锁与冲突边绝不可在提交瞬间删除**，必须保留直至所有在其存活期间并发的读写事务全部结束，否则会漏检延迟暴露的危险结构！4）**精准受害者选择与 `RO-Safe` 只读优化**：检测到危险结构时绝不可中止已经提交的事务，必须中止其中尚未提交的冲突事务（若 `T2` 未提交优先中止 `T2`，否则中止仍活跃的 `T1`）；同时实现 `READ ONLY DEFERRABLE` / `RO-Safe` 判定：若只读事务的快照早于所有可能产生出边的并发读写事务提交，则该只读事务永不卷入危险结构且无需回滚。为关系型存储引擎交付可导入的 Go SSI 谓词锁、危险结构检测与 MVCC 事务并发验证核。验收覆盖经典 2-事务 Write Skew 精确中止一方、3-事务 Read-Only Anomaly（含 `T3` 先提交后延迟检测！）拦截、提交后 `SIREAD` 延迟清理防漏检、锁粒度升级保持完备以及 `RO-Safe` 只读免中止。

## 交付物

可导入的 Go 包 `ssidb`（module `prescreen.local/ps-28e0491517`），在内存 MVCC 存储之上实现 PostgreSQL 风格的 SSI 并发控制核。

| 文件 | 职责 |
| --- | --- |
| `ssidb/engine.go` | 内核、配置、事务生命周期、快照、`RO-Safe` 判定 |
| `ssidb/locks.go` | SIREAD 谓词锁（元组/索引叶页/表）与粒度升级、反向锁索引 |
| `ssidb/ssi.go` | rw 反依赖边、危险结构判定、受害者选择、提交前防御检查 |
| `ssidb/txn.go` | MVCC 多版本、`Get/Put/Delete/Scan/Commit/Rollback`、延迟清理 GC |
| `ssidb/errors.go` | 40001 序列化失败错误族与 `IsSerializationFailure` |
| `ssidb/example_test.go` | 可运行的导入示例（`go test -run Example`） |
| `ssidb/ssidb_test.go` | 全部验收场景与并发压力测试 |

## 快速上手

```go
import "prescreen.local/ps-28e0491517/ssidb"

e := ssidb.NewEngine(ssidb.DefaultConfig())
t := e.Begin()                 // 或 BeginReadOnly / BeginReadOnlyDeferrable
v, ok, err := t.Get("k")       // 点查：登记元组 SIREAD 锁
rows, err := t.Scan("a", "z")  // 范围扫描：元组 + 索引叶页锁，防幻读
_ = t.Put("k", "v")            // 谓词冲突只建 rw 边，绝不阻塞
if err := t.Commit(); errors.Is(err, ssidb.ErrSerialization) {
    // 40001：调用方重试整个事务即可
}
```

## 四大机制的落地映射

1. **SIREAD 谓词锁与升级**：`predicateLocks` 按元组/页/表三级登记，全部为非阻塞内存结构。点查 (`Get`) 取元组锁；范围扫描 (`Scan`) 对每个可见键取元组锁、对访问到的索引叶页（FNV 哈希桶）取页锁，空范围保守升级为表锁。单事务元组锁超过 `MaxTupleLocks` 合并为页锁，页锁超过 `MaxPageLocks` 升级为表锁；升级始终是覆盖超集，冲突检测保持保守完备（见 `TestLockEscalation_Completeness` 与 `TestRangeScanPhantomProtection`）。
2. **双向 rw 反依赖边**：写路径 `write -> readerConflict` 在命中他人 SIREAD 锁时建 `reader -> writer` 边；读路径 `observeReadLocked`/intent 处理在发现对快照不可见的已提交新版本或活跃写意向时建同样方向的边。边在建立瞬间即触发危险结构检测。
3. **危险结构与延迟清理**：`checkDangerousLocked` 判定枢纽同时具备入边与出边；`T1==T3` 即 2-环 Write Skew，否则要求 `T3.commitID` 对 `T1` 与枢纽的快照均不可见（“出边冲突先提交”的 3-事务只读异常）。提交事务的锁与边不会在提交点删除，`gcLocked` 仅在其结束序号早于所有现存活跃事务起始号（等价 PostgreSQL `SxactGlobalXmin` 规则）时才回收（见 `TestSIREADLocksRetainedAfterCommit`、`TestReadOnlyAnomaly_DelayedDetection`）。
4. **受害者选择与 RO-Safe**：`pickVictimLocked` 永不选中已提交事务——优先中止未提交枢纽，其次活跃的 `T1`，最后活跃 `T3`；`precommitDangerLocked` 在授予提交点前防御性复检。`BeginReadOnlyDeferrable` 阻塞至该只读事务成为最老活跃事务（无更老读写事务）才标记 `safe`，此后不取谓词锁、不建边、不会回滚（见 `TestReadOnlyDeferrable_ROSafe_NeverAborts`，对照 `TestNormalReadOnly_StillAborted` 证明这是精确豁免而非漏检）。

## 验收测试

```bash
go test ./... -race -count=1
```

- `TestWriteSkew_TwoTransactions_ExactlyOneAborted`：2-事务 Write Skew 恰好中止一方。
- `TestReadOnlyAnomaly_ThreeTransactions` / `TestReadOnlyAnomaly_DelayedDetection`：3-事务只读异常，含 `T3` 先提交、边延迟闭合的拦截。
- `TestSIREADLocksRetainedAfterCommit`：提交后 SIREAD 锁/边延迟清理，防止漏检。
- `TestLockEscalation_Completeness`：元组→页→表升级后幻写仍被捕获且写不被阻塞。
- `TestReadOnlyDeferrable_ROSafe_NeverAborts`：RO-Safe 等待、免锁、免中止。
- 另含 SI 基线（无脏读、可重复读、first-updater-wins）、幻读防护与 16 goroutine 并发压力测试。

## 设计边界

- 内核为内存实现，`mutex + cond` 保证线程安全；写意向采用 first-updater-wins（不阻塞等待行锁，直接返回可重试的 40001）。
- 索引叶页用键的 FNV-1a 哈希桶建模，`PagesPerTable` 与升级阈值均可通过 `Config` 调小以确定性测试。
- 序列号在共享单调计数器上分配；快照可见性用独立的 `commitID/snapshotSeq` 判定，事务回收用起始/结束序号判定，二者语义分离。
