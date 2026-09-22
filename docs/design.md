# 可靠交付层设计

## 1. 坐标：epoch、环绕序号与绝对下标

每个分区是一条独立的事件流。源端给每个事件一个 `(epoch, seq)`：

- `epoch` 单调递增，标识源端故障切换 / 源流重置；
- `seq` 是线路序号，按分区模数 `modulus` 环绕；
- 交付层内部维护一个**永不环绕**的绝对下标 `abs`。

`abs` 从 `PartConfig.initial_abs` 开始（默认 seq 1 对应 abs 0），稳定交付 ID
即 `dlv:<partition>:<abs>`。因此崩溃恢复、重发、纠正通知都可以复用同一个 ID。

映射规则（`rdl/sequence.py`）：以交付头 `head_abs` 期望的线路序号为基准，
计算输入序号的前向环距 `fwd` 与后向环距 `modulus-fwd`，取距离较小的候选。
只要“未决跨度 < modulus/2”，映射就无歧义。无歧义区的边界即 **reorder window**
（`modulus//2 - 1`）；窗口外的输入被拒绝（源端重试），而不是被当成旧事件。
硬性的 live 容量上界保证未决跨度永远落在窗口内：对于卡住的分区，配置的
`modulus` 取大于两倍硬容量（场景中 P2：modulus=32, cap=9）。

epoch 不重置 `abs`：源端切换后流仍是同一条流，新 epoch 在下一个 abs 接续。
每个观测到的 epoch 记录一个 `EpochAnchor`（该 epoch 首次出现的 abs/seq），
用于比较跨 epoch 的迟到事件。

## 2. 两个相互独立的水位

- **交付位 `head_abs`**：下一个可向下游交付的位置；
- **确认位 `low_abs`**：连续确认前缀的上界。

二者可以分离：已交付未确认的事件停留在槽位集合里，崩溃后重新投递
（at-least-once），但交付 ID 不变。确认前缀之下的槽位会被裁剪，spool 同步
压缩；确认水位还单独原子落盘到 `watermark.json`，因此即使“确认已落盘但 spool
压缩尚未发生”就崩溃，恢复时也不会把已确认事件误判为未确认。

交付头只随**真实事件的 DELIVERED 提交**推进。缓冲中的 skip 槽位于头之前时，
头并不越过它；当缺口之后的真实事件交付时，DELIVERED 提交把头连带跳过的 skip
一并带过，并在同一提交点之后把对应证明标记为 crossed（崩溃时整组回滚）。
这保证了“缺口未填平/未跳过，3、4 已收到也不能越过”的硬约束：泵在遇到缺失
槽位时立即停下，返回 `blocked="gap"`。

## 3. 乱序缓冲与内存超额溢写

- 输入按 `abs` 进入有序槽位表；泵严格按 `abs` 升序交付。
- 接纳一条新事件时，载荷先追加到每分区的 `spool.log`（JSON line，
  append + flush + fsync），再进入内存槽位。spool 是载荷的持久副本与内存超额
  时的溢写落点。
- 当内存驻留载荷数达到 `memory_slots`，新接纳事件只驻留磁盘（`cached=False`），
  泵交付时按需从 spool 读回。
- live 槽位数达到 `capacity_live` 时直接背压拒绝
  （`CapacityExceededError`），因此**磁盘使用也有上界**，长期卡住的分区不会
  让容量失控。
- 注入的溢写失败会撤销本次字节并返回 `spill_flush_failed`：内存状态不变，
  源端必须重试；重试接纳得到同一个 abs。
- 确认前缀推进后，spool 在低水位以下压缩；checkpoint 把完整逻辑状态原子写入
  `snapshot.json` 并清空 WAL，保证两份日志都不会无限增长。

## 4. 跳过证明（skip proof）

证明绑定**来源 epoch** 与**缺口范围**（起止 seq，内部换算为连续 abs 区间）：

- 只能对当前交付头上的真实缺口签发；范围起点必须恰好是缺口 seq；
- 签发时校验 epoch 必须等于前沿 epoch（错误 epoch 的证明被拒绝）；
- 范围内若已存在真实事件，拒绝签发（永远不能静默丢弃数据）。

签发后区间内合成 skip 槽。**越过之前**证明可撤回：撤回删除合成槽、恢复缺口，
不产生冲突；此时真实事件到达即正常填补并交付。越过之后：

- 撤回证明 → `proof_revoked_after_cross` 显式冲突；
- 原事件（同 epoch）到达 → `late_event_after_skip` 显式冲突；
- 其它 epoch（更新或更旧）在同一位置到达 → `epoch_divergence_at_gap`
  显式冲突。

任何一种情况都**不会静默交付，也不会静默丢弃**：冲突登记在持久的冲突台账中，
必须显式决议：

- `confirm_skip`：维持跳过，向下游发出 `skip-confirmed` 纠正通知；
- `reinstate`：把该事件作为带外纠正通知（`reinstate`）交付，使用稳定的
  `corr:<partition>:<ordinal>` ID，正常走确认。

## 5. 持久化、崩溃与恢复

每个分区目录：

| 文件 | 内容 |
| --- | --- |
| `partition.json` | 静态配置与属主 |
| `spool.log` | 已接纳事件载荷（确认水位以下压缩） |
| `wal.log` | 状态转移（锚点、签发/撤回、交付、确认、冲突、决议、越过标记、迁移） |
| `snapshot.json` | 完整逻辑状态的原子快照（checkpoint 后 WAL 清空） |
| `watermark.json` | 确认水位的原子标记 |

所有提交遵循“先写磁盘再改内存”。WAL 提交支持注入**提交前崩溃**：字节回滚、
`CommitCrash` 抛出，内存管理器随即死亡；恢复时该提交不可见。于是：

- 交付先落 WAL、确认后落 WAL；确认提交前崩溃 → 恢复后重投，ID 不变；
- 已确认事件恢复后绝不重投（watermark + 槽位状态双保险）；
- 交付头带过 skip 的那一组记录在崩溃时整体消失，证明保持未越过、可撤回。

恢复顺序：读 snapshot（若有）→ 读 watermark 抬升确认水位 → 从 spool 重建
事件槽（已确认水位以下跳过；低于 head 的未确认槽恢复为 delivered 待重投）→
顺序重放 WAL → 压缩。

## 6. 热迁移

`migrate_partition` 是一个受控、分步、可观测的事件序列：

1. **冻结（fence）**源属主：摄入/确认/泵全部拒绝；
2. checkpoint 后把 `partition.json / snapshot.json / spool.log / wal.log /
   watermark.json` 复制到迁移 bundle（fsync）；
3. 删除旧属主目录并从 bundle 在新属主下重建；
4. 解冻恢复流量。

未决缺口、skip 槽（及是否 crossed/revoked）、证明台账、开放冲突、纠正通知、
交付头、确认水位、在途交付集合全部随 snapshot 一起带走。轨迹中的
`MIGRATE` 事件对迁移前后状态做逐字段等价核对（`MIGRATE_CHECK`）。迁移后泵会
以原 ID 重投所有已交付未确认事件；在新属主上、越过之前撤回证明依然有效。

## 7. 多分区独立性

管理器为每个分区维护独立的容量账（live、内存、在途）。一个分区长期卡住只
冻结它自己的交付位：其它分区的摄入、泵、确认与迁移照常推进。卡住分区在硬性
live 容量处背压，内存载荷数与 spool 记录数同时有界；下游恢复后按序排空，
最终 live/spool/在途全部归零。

## 8. 不变量（由 `scripts/verify_trajectory.py` 独立核验）

I1 缺口纪律；I2 按序与环绕；I3 交付 ID 稳定；I4 确认幂等且已确认不重投；
I5 证明 epoch 绑定/撤回/冲突必须显式纠正；I6 溢写失败与容量有界；
I7 崩溃恢复语义；I8 迁移状态等价；I9 分区独立性；I10 无丢失、无重复、
被跳过的 seq 永不作为事件交付。
