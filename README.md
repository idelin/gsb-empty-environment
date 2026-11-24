分区顺序写盘的快照会钉住旧内容。写入路径需要可导入的 Go 提交规划器：盘上至多五个 zone，每区八页，只能尾部追加；有效数据以不可变内容 ID 标识，一个 ID 可被多个命名快照和活动根同时引用。根表只保存 ID 到物理位置的映射。每次发布根表用双槽原子替换，盘固件可能在替换后用当前或上一槽启动，直到收到显式确认才丢掉上一槽；未确认前不能再次发布。两个可启动槽中任一所引用的物理页都不得重置。内部迁移允许先复制一份已有内容、后续一起发布多个逻辑根的新物理位置；尚未发布的副本崩溃后不可靠。输入为有序客户操作（原子改写一组逻辑页、建快照、删快照）。对客户只有确认后才能应答；崩溃后已应答的操作必须在所有可启动槽可见，未应答操作允许重试且不能被重复应用。每次无故障执行中可以额外发布纯物理重定位，不改变逻辑根。以内部复制页数、再以擦区次数为目标，给定精确最优无故障计划或返回无解；实际执行器须在任一动作后断电时从两种允许的启动槽各自恢复并继续完成客户序列，不要求崩溃后的额外工作最优。至多八个物理内容 ID、六条客户操作。交付真正实现持久/易失边界的模拟块设备，以独立小状态搜索比较成本；单测只要上一槽仍钉住某区，就算当前根已全部迁走也不能擦，还需验证同一内容跨快照共享、复制后未发布即崩溃、确认后回复丢失再投递。
# zoned snapshot commit planner

An importable Go write path for an append-only zoned device whose snapshots
pin old content. Three packages separate the pure planner, the simulated
persistent/volatile block device, and the crash-safe executor.

## Geometry and rules

- At most `zplanner.MaxZones` (5) zones, `zplanner.PagesPerZone` (8)
  tail-appendable pages each. Pages cannot be overwritten; a zone is reused
  only after a reset.
- Live data is named by immutable content IDs (`CID`). One CID can back many
  named snapshots and active roots at once; the root table only stores the
  CID-to-physical map, so sharing costs a single physical page.
- Publishing a root table atomically swaps between two slots. After the swap
  both slots are bootable and firmware may start from either one until an
  explicit `Ack` drops the previous slot. A second publish is rejected while
  the first is unacknowledged (`zdevice.ErrPublishPending`).
- A physical page referenced by either bootable slot pins its zone; resetting
  such a zone fails (`zdevice.ErrPinned`) even after the active roots have
  migrated away.
- Internal migration copies an existing CID first, then publishes the new
  physical locations of several logical roots together. Unpublished copies
  are volatile and discarded on power loss.

## Packages

- `zplanner` — exact-optimal fault-free planning over abstract state via DFS
  with memoization and branch-and-bound. The objective is lexicographic:
  minimize internal copied pages first, then zone resets. New client writes
  consume capacity but carry no optimization cost. Returns
  `*zplanner.ErrInfeasible` when the sequence cannot fit. `ValidatePlan`
  independently replays a plan against every device rule.
- `zdevice` — the simulated block device. Mutations change only in-memory
  state until an explicit `Flush` writes a CRC-protected gob image;
  `PowerOff` drops unflushed state, modeling sudden power loss. Two slots,
  `Swap`, `Ack`, append-only zones and pinned resets are all enforced here.
- `zexec` — the executor. `Resume` boots a chosen slot, acknowledges a
  retained publish when needed, and replans the remaining operations.
  Client replies are sent only after the slot Ack is durable, so
  acknowledged operations are visible from every bootable slot. Redelivery
  of an acknowledged operation (`ProcessOp`) is answered as a duplicate and
  never reapplied.

## Crash recovery

After power loss the test harness powers on from each allowed slot. `Resume`
  acknowledges the unacknowledged publish (safe because any work exclusive to
the dropped slot is redone), rebuilds the abstract planner state from the
booted table and current zone fill, and replans. Crash work need not be
optimal; only the fault-free plan returned by `zplanner.Plan` is.

## Tests

Run all tests, including a crash-after-every-device-action matrix from both
slots:

```
go test ./...
```

Coverage highlights:

- a zone pinned by the previous slot cannot be reset even when the current
  roots have fully migrated (`zdevice.TestPinnedByPreviousSlotAfterCurrentRootMigrates`);
- one physical CID shared across snapshots (`zexec.TestSharedContentAcrossSnapshots`);
- an unpublished internal copy is lost on crash (`zexec.TestUnpublishedCopyLostOnCrash`);
- a lost reply after acknowledgment redelivers without reapplication
  (`zexec.TestAcknowledgedOpRedeliveredAfterReplyLoss`);
- power loss after every low-level action, recovered from both boot slots
  (`zexec.TestCrashAfterEveryActionFromBothSlots`).

The supported problem size is at most `zplanner.MaxCIDs` (8) distinct
content IDs and `zplanner.MaxOps` (6) client operations.
