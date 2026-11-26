快照隔离下两个并发事务各自读到对方修改前的数据并在不重叠键上写入时，传统写锁无法拦截写偏斜（Write Skew）；引入可串行化快照隔离（SSI）后，既要捕捉相邻的读写反依赖链（rw-antidependency），又不能在只读事务或已中止分支上产生误杀。给事务管理器交付可导入的 Go SSI 冲突检测包。组件维护事务生命周期（Begin、Read、RangeRead、Write、Prepare、Commit、Abort）以及行级/索引范围 SIREAD 锁，并支持锁升级（单事务在同表持有的行级 SIREAD 锁超过阈值时升级为表级锁）。核心规则：当事务 T1 读取了键或范围并被对 T1 不可见的并发事务 T2 写入时，记录有向边 T1 -rw-> T2（标记 T1 存在出冲突、T2 存在入冲突）；提交或写入检测到连续两跳危险结构 T1 -rw-> T2 -rw-> T3 且 T3 先于 T1 提交（含 T1==T3 的两事务环）时，必须中止枢纽事务或当前提交者以保证可串行化；只读事务在确认无并发读写事务比其更早提交并形成入冲突前不可过早释放 SIREAD，且满足安全快照（Safe Snapshot）条件的只读事务免于参与危险结构中止；事务已中止后，其引发的 rw 边须撤回且不得继续阻塞其他事务提交，但已提交事务的 SIREAD 锁须保留至所有与其并发的活跃事务全部结束。验收用并发调度序列覆盖两事务写偏斜环、三事务危险结构（T3 先提交触发枢纽中止）、只读安全快照豁免、已中止事务误报清除、SIREAD 升级为表锁后新冲突捕获以及已提交旧事务 SIREAD 过早回收导致的漏检场景。

## ssi 包

`ssi` 是一个可导入的内存型可串行化快照隔离冲突检测包，位于 `ssi/`。

### 快速开始

```go
import "prescreen.local/ps-44a031b2eb/ssi"

mgr := ssi.NewManager(ssi.WithEscalationThreshold(8))

t1 := mgr.Begin()
t2 := mgr.Begin()

if _, _, err := mgr.Read(t1, "doctors", "a"); err != nil { /* ... */ }
if _, _, err := mgr.Read(t2, "doctors", "b"); err != nil { /* ... */ }

if err := mgr.Write(t1, "doctors", "b", "off"); err != nil { /* ... */ }
if err := mgr.Write(t2, "doctors", "a", "off"); err != nil { /* ... */ }

// 写偏斜环 T1 -rw-> T2 -rw-> T1：其中一个提交返回 ssi.ErrSerialization。
if err := mgr.Commit(t1); errors.Is(err, ssi.ErrSerialization) {
    mgr.Abort(t1)
}
```

### 能力

- 生命周期：`Begin`（含 `WithReadOnly`）、`Read`、`RangeRead`、`Write`、`Prepare`、`Commit`、`Abort`。
- SIREAD 谓词锁：行级、索引范围级，以及超过阈值后升级的表级锁（`WithEscalationThreshold`）。
- rw 反依赖有向图：读取不可见写入或写入命中他人谓词锁时记录 `T1 -rw-> T2`，分别维护出/入冲突。
- 危险结构：检测两跳 `T1 -rw-> T2 -rw-> T3`，含两事务环与 `T3` 先提交的情形，优先中止枢纽，其次当前提交者。
- 安全快照：满足条件的只读事务不获取谓词锁、不建边、不参与中止。
- 中止撤回：中止事务的边、锁、写记录立即清除，不再阻塞他人。
- 锁保留：已提交事务的 SIREAD 锁保留到无任何并发活跃事务后统一回收。

### 验收测试

`ssi/ssi_test.go` 覆盖写偏斜环、三事务危险结构、只读安全快照（安全/非安全）、中止误报清除、行锁升级表锁、范围锁边界、提交锁保留漏检回归、Prepare 阶段检测、快照可见性及 goroutine 竞态调度。

```bash
go test ./...
go test -race ./...
```
