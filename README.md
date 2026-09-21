# 可靠交付层（Reliable Delivery Layer）

面向**重复投递、乱序到达**的多分区事件源的本地 Python 3.11 实现，仅用标准库。

核心保证：

- 分区缺口未闭合时绝不越过：3、4 已到达也停在 2，必须有绑定来源 **epoch + 缺口范围**的跳过证明（`SkipProof`）才能继续。
- 证明在尚未交付（gap 未被越过）前可以撤回；越过之后再收到原事件、或再撤销证明，进入**显式冲突 / 纠正流程**（隔离 + `drop` / `deliver` 纠正）。
- 序号按可配置模数**环绕**；交付游标携带环周期（cycle），交付 ID 为 `分区#epoch:cycle:seq`，跨崩溃 / 迁移对同一事件保持稳定。
- **交付位置与下游确认位置分开**（`deliver_next` vs `ack_next`）；未确认事件恢复后按原 ID 重发（at-least-once，下游按交付 ID 去重）。
- 乱序缓冲有内存上限，超额**溢写磁盘**；溢写失败给出背压（事件不丢失、可重试），内存 + 磁盘总量有界。
- 分区**热迁移**带走缺口（缓冲/溢写事件）、证明、已交付与已确认状态。

## 目录结构

| 路径 | 内容 |
| --- | --- |
| `reliable_delivery/events.py` | `Event`、环绕序号运算、稳定交付 ID |
| `reliable_delivery/proofs.py` | `SkipProof`、`Conflict` |
| `reliable_delivery/journal.py` | 显式 flush / crash 的追加日志（WAL） |
| `reliable_delivery/spill.py` | 溢写后端，含故障注入与容量上限 |
| `reliable_delivery/partition.py` | 单分区状态机（乱序缓冲、缺口、确认、冲突） |
| `reliable_delivery/layer.py` | 多分区层：操作、恢复重发、热迁移、交付轨迹 |
| `tools/verify_trace.py` | 交付轨迹核验程序（CLI + `verify_trace()`） |
| `tests/` | 可控事件测试（情景 / 恢复 / 迁移 / 溢写 / 环绕 / 核验器） |
| `examples/run_scenario.py` | 题目情景的可运行剧本，产物落在 `run/` |

## 关键模型

**位置。** 每个分区维护两条独立游标：

- `deliver_next` / `deliver_cycle`：下一个可交付序号（环周期在越过模数边界时 +1）；
- `ack_next` / `ack_cycle`：下游已连续确认的位置。

游标懒锚定到该 epoch 第一个到达的事件序号（源从 1 开始就从 1 交付）。

**乱序缓冲与溢写。** 到达事件若领先游标则进入缓冲；缓冲满时把离游标最远的事件溢写到
`spill/` 目录，排干时按序读回。每分区 `max_buffered` + `max_spilled` 有界，
超限抛 `CapacityError`，溢写写失败抛 `SpillError`——两种情况下事件都未被接受，调用方重试即可。

**跳过证明。** `authorize_skip(partition, epoch, gap_start, gap_end)` 生成
`{proof_id, partition, epoch, gap_start, gap_end, revoked, crossed, crossed_cycle}`：

- 证明只对所绑定 epoch 生效；`revoked` 后失效；
- 排干游标遇到缺口且存在覆盖该序号的有效证明时才越过，并记录 `crossed` 与发生越过的环周期；
- 越过之后：迟到的原事件 → `late_event_after_skip` 冲突并隔离；撤销证明 → `revoke_after_cross` 冲突；
- 冲突通过 `resolve_conflict(partition, id, "drop" | "deliver")` 显式纠正，`deliver` 作为带 `correction` 标记的交付下发；
- 旧 epoch 事件 → `stale_epoch_event` 冲突；新 epoch 的提前事件暂存，epoch 切换后处理。

**持久化与崩溃。** 每次状态变更先写日志记录（`ingest / skip_proof / revoke / ack / epoch / resolve / snapshot / migrate_out`），`flush()` 落盘，`crash()` 丢弃未落盘缓冲，`recover()` 重放日志重建全部状态，再把“已交付未确认”的事件按原交付 ID 重发。确认落盘前崩溃 → 事件被重发（ID 不变）。

**热迁移。** `export_partition(name)` 产出完整快照并在源端写迁出墓碑；`import_partition(bundle)` 以快照记录写入目标日志并重建状态（溢写事件按目标容量重新落盘）。目标端轨迹会补齐 epoch、携带的证明 / 交付 / 确认记录，可独立核验。

**交付轨迹。** 每次运行写 `trace.jsonl`（`open / epoch / skip / revoke / deliver / redelivery / correction / conflict / resolve / ack`）。核验程序检查：

1. 同一 `(partition, epoch)` 内交付连续，缺口只能被更早出现、同 epoch、未撤回的证明覆盖；
2. 交付 ID 至多正常交付一次，重复出现必须是同载荷哈希的 `redelivery`；
3. 确认只能引用已交付 ID（重复确认合法）；
4. 证明不允许在缺口越过后撤销。

## 运行

```sh
# 全部测试（标准库 unittest）
python3 -m unittest discover -s tests -v

# 题目情景剧本，产物在 run/node1、run/node2
python3 examples/run_scenario.py

# 独立核验交付轨迹
python3 -m tools.verify_trace run/node1/trace.jsonl
python3 -m tools.verify_trace run/node2/trace.jsonl
```

## 情景与故障覆盖（tests/test_scenario.py）

1. 1、4、3 乱序到达 → 只交付 1；仅确认 1（含重复确认）；
2. 授权跳过 2 → 交付 3、4；
3. 旧 epoch 的 2 迟到 → 冲突 → 纠正交付（ID `A#0:0:2`）；
4. 切换 epoch，新 epoch 的 0、2、1 交错，旧 ID 的重复确认无副作用；
5. 注入溢写失败 → 背压、重试成功，随后跳过 2 正常读出溢写事件；
6. 确认未 flush 即崩溃 → 恢复后未确认事件按原 ID 重发，已 flush 的确认不重发；
7. 分区 D 长期卡在缺口 2：缓冲 / 溢写打满后 `CapacityError`（容量有界），分区 A 继续交付；
8. D 热迁移到第二节点：缺口、未越过的 [7,9] 证明、确认状态随迁；在新节点授权跳过 2 后，携带的证明继续生效；
9. 两端 `trace.jsonl` 均通过核验程序。

其余测试覆盖恢复重放、迁移后目标端再崩溃、溢写存活崩溃、小环（模数 8）环绕与证明跨环，以及核验器对违规轨迹的负例检查。
