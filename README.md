# gdc — 按组维护 distinct 计数的增量聚合器

Python 3.11+，仅标准库，测试用 unittest。变更来自本地 JSON 夹具
（含事务 ID、表、主键、前后值镜像、提交序号），不接入消息平台。

## 语义

- 每个 `(组, 值)` 维护**支持计数**（supporter 行数）；组的 distinct 数 = 计数 > 0 的值个数。
- 同值两行删其一，支持计数减一但 distinct 仍计 1；**删除最后支持者才减 distinct**。
- 删除/更新使计数变负 = 缺事件，抛 `MissingEventError` 拒绝，状态不受污染。
- **null 策略显式**：`NullPolicy.IGNORE`（含 null 不计）或 `COUNT_AS_VALUE`（null 作独立值）；
  `null_equal=False` 时每个 null 出现按 `(主键, 列)` 区分为独立 distinct 值。
- 支持**多列组合 distinct** 与**条件过滤**（`row_filter` 同时作用于前后行镜像）。
- **跨组更新原子执行**：撤销旧贡献 → 加入新贡献，任一步失败按回滚日志整体回滚。
- 组内值数超阈值即**溢写**磁盘，访问时按需载入。
- **checkpoint 保存完整支持计数与输入去重集合**（`event_id` 集合）——
  仅保存输出 distinct 数不足以恢复（同值多行的支持计数会丢失）。
- `ReferenceModel` 把事件应用到全量行集合后整体重算，**独立核验**乱序、
  重复与恢复后的聚合器状态。

## 运行

```bash
python3 -m gdc.main                    # 运行样例（读 fixtures/sample_events.json）
python3 -m unittest discover -s tests  # 运行测试
```

## 运行样例输出

```
== 阶段1 夹具事件 == {'applied': 8, 'dedup_skipped': 1, 'filtered': 0}
== 阶段2 溢写文件 == ['group_12.pkl']
== distinct 数 ==
  ('DE',): 15
  ('US',): 2
== 支持计数快照 ==
  ('DE',): {('u5@x.de',): 1, ..., ('u19@x.de',): 1}
  ('US',): {('a@x.cn',): 2, (NULL,): 1}
== 恢复后重放旧事件 == 已按去重跳过, stats = {'applied': 0, 'dedup_skipped': 1, 'filtered': 0}
== 参考核验（原始聚合器）== 一致
== 参考核验（恢复聚合器）== 一致
== 缺事件检测 == 已拒绝: 支持计数将变负（缺事件）: group=('CN',) value=('ghost@x.cn',)
```

## 测试结果

```
Ran 21 tests in 0.007s
OK
```

覆盖：同值两行删一仍计 1、删最后支持者减一、负计数拒绝、输入去重、
异表忽略、null 两种策略与 null_equal 开关、多列组合 distinct、条件过滤、
跨组迁移及其双向回滚、溢写往返、checkpoint 保留支持计数与去重、
恢复后乱序/重复流的独立全量参考核验、以及"只按输出计数恢复"无法通过核验。

## 结构

- `gdc/events.py` — 变更事件模型（tx_id / table / pk / before / after / commit_seq）
- `gdc/aggregator.py` — 增量聚合器（支持计数、null 策略、原子迁移、溢写、checkpoint）
- `gdc/reference.py` — 独立全量集合参考核验
- `gdc/fixtures.py` — 本地 JSON 夹具加载
- `gdc/main.py` — 运行样例
- `fixtures/sample_events.json` — 样例事件（含重复投递、跨组迁移、异表事件）
- `tests/test_aggregator.py` — 21 个 unittest 用例
