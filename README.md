# 多分区事件源可靠交付层（RDL）

面向**重复投递、乱序到达、序号环绕**的多分区事件源，提供严格缺口纪律、
epoch 绑定的跳过证明、显式冲突/纠正流程、内存超额溢写、崩溃恢复与分区热迁移。
纯 Python 3.11+ 标准库实现，无第三方依赖。

## 快速开始

```bash
# 运行可控事件场景（在 runtime/ 下生成持久状态与轨迹）
python3 scripts/run_scenario.py

# 独立核验交付轨迹的全部不变量（退出码 0 即通过）
python3 scripts/verify_trajectory.py runtime/trajectory.jsonl

# 组件级单元测试
python3 -m unittest discover -s tests
```

## 场景覆盖（`scripts/run_scenario.py`）

- **P0（mod 16）主故事**：1、4、3 乱序到达，只交付 1；错误 epoch 的跳过证明
  被拒；签发绑定 epoch=1、seq=2 的证明后，3、4 按序交付；随后交错到达
  同 epoch 迟到的 2、旧 epoch 的 2、新 epoch 的 2，以及重复确认；两类冲突
  分别走 `confirm_skip` / `reinstate` 显式纠正；注入“确认落盘前崩溃”，
  恢复后 3、4 以同一 ID 重投，1 不重投。
- **P1（mod 8，从 seq 6 起）**：6、7、0、1 跨环绕按序交付；强制内存超额溢写
  到 spool，注入一次**溢写失败**并被源端重试；在未越过缺口前带着证明与在途
  确认状态热迁移到 `worker-2`；迁移后撤回证明、真实事件补洞继续交付。
- **P2（mod 32，硬容量 9）长期卡住**：内存 2 槽、在途 3 槽；卡顿时 live 顶到
  硬容量后背压，内存与磁盘同时有界；期间 P0/P1 持续推进；恢复后按序排空，
  所有容量账归零。

## 轨迹（trajectory）

每个可控步骤在 `runtime/trajectory.jsonl` 中追加一行 JSON，类型包括
`INGEST / DELIVER / REDELIVER / ACK / GRANT / REVOKE / SKIP_CROSSED /
CONFLICT / RESOLVE / CORRECTION / SPILL_FAIL / CRASH / RECOVER /
MIGRATE / MIGRATE_CHECK / STUCK_BEGIN / STUCK_END / SAMPLE` 等。
核验器**不依赖库代码**，只读取轨迹重放断言，适合作为交付审计材料。

## 代码布局

```
rdl/
  sequence.py   # 环绕序号/epoch -> 绝对下标映射
  storage.py    # spool、WAL、原子快照、水位标记、故障注入
  model.py      # 配置、事件、结果与冲突视图
  partition.py  # 槽位、缺口、证明、冲突、纠正、泵、确认（核心状态机）
  manager.py    # 多分区管理、热迁移、崩溃/恢复、容量采样
  harness.py    # 可控事件测试台 + 轨迹记录
scripts/
  run_scenario.py       # 端到端场景
  verify_trajectory.py  # 轨迹不变量核验程序
tests/                  # unittest 组件测试
docs/design.md          # 设计说明（含不变量 I1–I10）
```

## 关键语义摘要

- 缺口未填平或未持证明时，泵在缺口处停止，后续事件即使已收到也不交付。
- 交付位 `head_abs` 与确认位 `low_abs` 分离；恢复重投使用稳定 ID
  `dlv:<分区>:<abs>`。
- 跳过证明在越过缺口前可撤回；越过之后的撤回或原事件/异 epoch 事件到达，
  一律进入显式冲突，必须 `confirm_skip` 或 `reinstate` 决议。
- 热迁移带走缺口、证明、冲突、纠正、在途与确认状态。
- 硬 live 容量对内存与磁盘同时上界；一个分区卡住不影响其它分区进展。
