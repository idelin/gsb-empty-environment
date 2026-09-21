# 可修订告警状态机（Revisable Alert State Machine）

纯 Python 3.11，零第三方依赖；存储用标准库 `sqlite3`（落盘文件，outbox
重启即真实进程重启），数据全部合成。

## 核心设计

**两轴分离**
- *真实故障轴*：`pending / firing / suppressed / resolved`，是事件时间
  （event time）上样本与规则的纯函数，存在可随时删除重建的派生表中。
- *通知轴*：outbox 追加事件 `OPEN / RESOLVE / RETRACT / REPLACE`，确认后
  进入 `delivered`。纠正只能**新增**事件，审计记录永不消失。

**状态语义**（`alerting/domain.py` 的 `evaluate`）
- 进入：值 `> enter_at` 且持续 `duration` 个事件时间单位（稀疏样本合法，
  无观测不等于恢复）；成熟时刻 = `start_t + duration`。
- 退出：值 `<= exit_at`；滞回区 `exit_at < value <= enter_at` 保持episode。
- `suppressed`：成熟且当前被抑制窗口覆盖；抑制中成熟不会通知；抑制解除时
  故障仍在 → 解除点 OPEN；抑制中已恢复 → 静默结束，永不补发过时故障。

**版本化与追溯**
- 阈值/抑制规则都是不可变版本：“修改” = 插入新版本（`valid_from` 可在
  过去）；“回滚” = 追加一个复制旧参数的新版本（`rollback_threshold` /
  `rollback_suppression`）。规则版本切换处自动切分 episode。
- 重算后按半开区间 `[start, end)` 做新旧 episode 配对，输出
  `(alert_id, revision)` 的 `created / replaced / retracted`，每个 revision
  带 `supersedes` 指针和变更原因（`sample-correction`、`threshold-rollback`、
  `suppression-edit` 等）。

**增量 vs 全量**
- 增量：按指标裁剪 `canonical_cache`，仅重放受影响尾部。
- 全量：`Engine.replay(path)` 从不可变 `input_log` 全新重建。
- 差异测试在每一步操作后断言 告警状态 / 通知内容 / 修订审计 三者完全一致。

**可确认 outbox（`alerting/outbox.py`）**
- 两阶段：claim（fence 落盘）→ transport 发送 → 写 `delivered` 并删 outbox。
- `CrashAt.BEFORE_SEND / AFTER_SEND` 注入崩溃；重启按 dedupe 键
  `alert_id:revno:kind` 幂等重试，fence 丢弃过期回执：不重复、不丢失。

## 运行

```bash
python3.11 -m alerting.demo              # 中文端到端演示
python3.11 -m unittest discover -s alerting/tests -v
```

## 验证的场景
1. 进入阈值 10、持续 5：`t=0:11`、`t=6:12` 触发；补 `t=3:8` 后旧告警
   以新 revision 撤销（`OPEN` 后追加 `RETRACT`，审计保留）。
2. 抑制期间故障恢复：解除抑制后不补发。
3. 三条交叠抑制窗口、阈值规则回滚、同时间采样更正、outbox 重启。
4. 增量状态与完整历史重算逐步对照（含多指标/多规则随机流，多随机种子）。
5. 崩溃注入：“已记录尚未确认”“发送后确认前宕机”“纠正消息发送中宕机”。

## 模块
- `alerting/domain.py`：纯函数状态机（样本、版本、episode、覆盖区间）。
- `alerting/engine.py`：SQLite 持久化、增量重算、修订 diff、outbox 对账、
  全量 replay。
- `alerting/outbox.py`：可确认投递泵 + 崩溃注入。
- `alerting/tests/`：状态机、引擎、outbox 重启、增量/全量差异测试。
