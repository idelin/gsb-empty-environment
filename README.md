# distinct-agg：分组支持计数 / distinct 计数引擎

Python 3.11+，仅标准库（`sqlite3`、`json`、`unittest`）。变更来自本地夹具
（JSONL，含事务 ID、表、主键、前后值、提交序号），不接消息平台。

## 语义

- **支持计数**：每个 `(组, distinct值)` 记录支持者行数；组的 distinct 数 =
  支持计数 > 0 的值个数。同值两行删一仍计 1，删最后一条才减。
- **缺事件拒绝**：计数将变负、前镜像与存储贡献不符、提交序号回退、重复插入
  均抛 `MissingEventError`，且状态不被改动。
- **null 策略**：`null_equal=True` 同列所有 null 折叠为一个 distinct 值；
  `False` 每行的 null 按主键区分、各自独立。
- **多列组合 distinct**：`distinct_cols` 组成复合键；**条件过滤**：
  `filters=[(col, op, value), ...]`，前/后镜像分别判定，行可因更新进出过滤范围。
- **跨组更新**：原子撤销旧贡献再加入新贡献，失败回滚。
- **输入去重**：按 `(txn_id, table, pk, commit_seq)` 去重，重复投递返回
  `"duplicate"`。
- **状态溢写**：`SpillMap` 内存缓存有界（`max_cached`），超出按 FIFO 溢写
  sqlite；`store_path` 可落盘。
- **checkpoint**：`engine.checkpoint(path)` 持久化支持计数、主键贡献、已见
  事件集合、提交序号与配置；`DistinctCountEngine.restore(path)` 恢复续跑。
  仅保存输出计数不足以恢复（见测试
  `test_output_counts_alone_not_sufficient_for_recovery`）。
- **独立参考核验**：`reference.py` 对原始事件去重、按提交序号排序后重放为
  全量行集合再一次性计算，与增量引擎无关；`engine.verify(events)` 对乱序、
  重复投递健壮。

## 运行

```bash
python3 main.py                          # 运行样例（fixtures/changes.jsonl）
python3 -m unittest discover -s tests -v # 测试
```

## 运行样例输出

```
== 输入统计 == {'applied': 9, 'duplicate': 1}
== 一次性跑完 == {'ops': 2}
== checkpoint 恢复续跑 == {'ops': 2}
== 两路径一致 == True
== 参考核验(乱序+重复) == True
== ops 组 null 值的支持计数 == 2 (D、F 两行同值 null，distinct 仍计 1)
```

## 测试结果

```
Ran 17 tests in 0.033s
OK
```

覆盖：重复行增删、删最后支持者、负计数拒绝、序号回退拒绝、重复投递去重、
跨组原子迁移、前镜像不符、两种 null 策略、多列组合 distinct、条件过滤、
溢写、checkpoint 恢复续跑与去重保持、输出计数不足以恢复的反例、
乱序+重复参考核验、300 事件随机回放 × null 策略 × 过滤组合对照参考。

## 结构

- `distinct_agg/events.py` — 变更事件与 JSONL 夹具加载
- `distinct_agg/state.py` — 可溢写 KV（内存缓存 + sqlite）
- `distinct_agg/engine.py` — 增量引擎：支持计数、去重、checkpoint/恢复
- `distinct_agg/reference.py` — 独立全量集合参考
- `fixtures/changes.jsonl` — 本地变更夹具
- `main.py` — 运行样例；`tests/test_engine.py` — unittest
