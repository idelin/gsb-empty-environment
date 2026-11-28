RocksDB 风格的 Leveled LSM-Tree 在高并发写入下需要同时运行多个后台压实任务与子压实（Subcompaction）线程，调度器稍有不慎就会引发跨层键序错乱、范围墓碑丢失或永久写卡死：1）**层级放大评分与跨层文件锁无冲突选取**：每层计算紧迫度 `score`（`L0` 按文件数除以 `l0Trigger`，`L >= 1` 按剔除已在压实字节后的层大小除以目标字节阈值！）；调度器按 `score >= 1.0` 降序选层挑文件，并按规则扩展输入集（选中某 `L0` 文件时必须连带拉入全部与其键范围重叠的 `L0` 文件；再拉入 `L+1` 层全部与输入总范围重叠的文件，并尝试无冲突扩展同层相邻文件！）；若扩展集中的任意文件已被另一活跃压实任务锁定（或 `L0->L1` 已被占且键区间交叉），则绝不可强行启动——必须跳过该冲突候选转挑下一个无冲突候选！2）**Subcompaction 并发切分与范围墓碑（Range Tombstone）边界保真**：当单个压实任务输入总字节超过阈值时，依据输入与输出层各 SST 的最小/最大边界键切分为至多 `maxSubcompactions` 个互不重叠的闭开子键区间 `[b_i, b_{i+1})` 并发归并；最容易出错的陷阱是：若输入 SST 包含跨越多个子区间的范围墓碑 `[tStart, tEnd) @ seq`，切分器必须将该墓碑按每个子区间边界精确裁剪并下发给**所有与之相交的子压实**（绝不能只交给 `tStart` 所在的第一个子区间导致后续子区间内的旧键复活！），且仅当本压实目标层为底层（无更老层重叠键）且无活跃快照锚定时才允许物理丢弃旧版本与范围墓碑！3）**`WriteStall` 软/硬水位状态机**：实时监控 `L0` 文件数与全局待压实字节数，精准驱动 `Normal <-> Delayed(软限速) <-> Stopped(硬停写)` 状态迁移与解除。为 LSM 存储引擎交付可导入的 Go 并发压实调度、Subcompaction 切分执行与 WriteStall 控制器包。验收覆盖并发压实文件锁互斥与跳过挑次优、跨多子区间的范围墓碑裁剪零复活、活跃快照保活旧版本、安装新版本后全层 `L>=1` 键严格有序互不重叠以及 `WriteStall` 三态精准切换。

## 实现

模块根包为 `lsm`，同时提供 `lsm`、`compaction`、`subcompaction` 和
`writestall` 四个可直接导入的入口（后者均为根包类型的薄别名）。

```go
import "prescreen.local/ps-b366051703"

opts := lsm.DefaultCompactionOptions()
opts.L0Trigger = 4

scheduler := lsm.NewCompactionScheduler(versionSet, opts)
task := scheduler.Pick()          // 已锁定输入文件与输出键区间
outputs, err := scheduler.ExecuteSubcompactionsAndInstall(ctx, task,
    lsm.SubcompactionOptions{
        MaxSubcompactions:      4,
        ThresholdBytes:         64 << 20,
        UseVersionSetSnapshots: true,
    })
```

`CompactionScheduler.Pick` 按 `score >= 1.0` 降序尝试层级；候选若与活跃
任务的文件锁或输出键区间冲突，会跳过并继续下一个候选。`Finish` 先校验
输出文件在 `L >= 1` 上严格有序且互不重叠，再原子安装新版本；校验失败时
会自动释放该任务锁，避免压实资源永久卡死。

`PlanSubcompactions` 生成的每个子区间都会收到按该区间裁剪后的范围墓碑
副本。`MergeCompaction` 仅在 `Bottommost == true && len(Snapshots) == 0`
时物理丢弃旧版本和范围墓碑。

```go
stall := lsm.NewWriteStallController(lsm.WriteStallConfig{
    L0SlowdownTrigger:              20,
    L0StopTrigger:                  36,
    PendingCompactionSlowdownBytes: 2 << 30,
    PendingCompactionStopBytes:     4 << 30,
})
state := stall.Update(l0Files, pendingCompactionBytes)
err = stall.WaitForWrite(ctx) // Stopped 时阻塞，Delayed 时限速一次
```

测试覆盖评分、锁冲突跳选、并发选取、范围墓碑跨区间裁剪、快照保活、
底层墓碑回收、版本有序性以及 WriteStall 三态和解除等待。运行：

```sh
go test ./... -race
```
