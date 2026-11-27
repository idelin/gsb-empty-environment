# mvcc — 32 位回绕 MVCC 与 VACUUM FREEZE 引擎

纯标准库实现的 PostgreSQL 风格堆页 MVCC 可见性判定与冻结清理引擎，
可直接 `import` 到存储层代码中。

## 核心抽象

- `XID`：32 位无符号事务号。`InvalidXid=0`、`BootstrapXid=1`、
  `FrozenXid=2` 为特殊常量，固定排在普通事务号 `[3, 2^32-1]` 之前，
  绝不参与模 `2^32` 运算；普通事务号用有符号差值
  `int32(a-b) < 0`（`XidPrecedes`）比较，天然支持 `0xFFFFFFFF -> 3` 回绕。
- `Clog`：内存版事务提交日志，实现 `TxnStatusProvider`，
  未登记事务一律保守视为 `StatusInProgress`。
- `MultiXactStore`：内存版多事务锁组存储。成员状态区分纯锁
  （`ForKeyShare`/`ForShare`）与修改（`ForNoKeyUpdate`/`ForUpdate`/
  `ForDelete`），创建时强制“至多一个修改成员”；编号同样 32 位环形分配。
- `Tuple` / `Page`：堆元组与堆页，`Infomask` 标志位取值与
  PostgreSQL `t_infomask` 对齐（含 `HEAP_XMAX_IS_MULTI`、
  `HEAP_XMIN_FROZEN` 等）。

## 可见性判定

`ClassifyTuple(tuple, oldestXmin, clog, mx)` 对应
`HeapTupleSatisfiesVacuum`，返回四态：

- `VerdictDead`：`xmin` 中止，或真正的删除事务已提交且
  `XidPrecedes(xmax, OldestXmin)`，对所有快照均死，可物理清除。
- `VerdictRecentDead`：删除已提交但仍可能被某活跃快照看到，禁止清除。
- `VerdictInsertInProgress`：插入事务运行中，受活跃快照保护。
- `VerdictLive`：元组存活；锁成员（含纯锁 MultiXact）与中止的
  修改成员从不杀死元组。

判定过程中解析到的 CLOG 状态会固化为元组提示位。

## VACUUM FREEZE

`VacuumPage(page, cutoffs, clog, mx)` 就地清理单个堆页：

1. 物理移除 `VerdictDead` 元组，其余元组保留；
2. 存活元组的已提交 `xmin` 若 `XidPrecedes(xmin, FreezeLimit)`，
   原地改写为 `FrozenXid`（严格小于、幂等）；
3. 过时普通 `xmax` 在事务结束后清除，运行中事务一律保留；
4. 过时 MultiXact 重打包：运行中的修改成员保护整组，
   其余情况只保留仍活跃的锁成员，全部结束则整体清除并回收编号；
5. 返回 `AllVisible` / `AllFrozen`，可经 `VisibilityMap.ApplyVacuum`
   落盘为 VM 位。

VM 位判定规则：

- `ALL_VISIBLE`：页内无死元组，且每个存活元组的 `xmin` 已提交并
  早于 `OldestXmin`、不存在未决删除（纯锁不算未决删除）。
- `ALL_FROZEN`：在 `ALL_VISIBLE` 基础上，每个存活元组 `xmin` 均为
  `FrozenXid`，且没有任何残留 MultiXact 或有效 `xmax`。
- 空页两位置位；`ALL_FROZEN` 永远以 `ALL_VISIBLE` 为前提。

## 最小示例

```go
clog := mvcc.NewClog()
clog.Set(mvcc.MaxXid, mvcc.StatusCommitted) // 回绕前的老插入事务

mx := mvcc.NewMultiXactStore()
page := &mvcc.Page{BlockNo: 0, Tuples: []*mvcc.Tuple{
    {Xmin: mvcc.MaxXid, Xmax: mvcc.InvalidXid},
}}
cutoffs := mvcc.Cutoffs{
    OldestXmin:      10,
    FreezeLimit:     5,
    MultiXactCutoff: 1000,
}

result, err := mvcc.VacuumPage(page, cutoffs, clog, mx)
// result.AllFrozen == true，page.Tuples[0].Xmin == mvcc.FrozenXid
```

## 测试

```bash
go test -race -count=1 ./...
```

覆盖回绕边界的 XID 比较与冻结、混合锁 + 中止/提交更新的
MultiXact 可见性与重打包、活跃快照防误删，以及
`ALL_VISIBLE` 与 `ALL_FROZEN` 的精确区分。
