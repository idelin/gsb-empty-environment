# cbound：按主体 ID 的贡献裁剪与分片合并（仅合成数据）

Python 3.11，仅标准库，测试用 `unittest`。无任何噪声添加；本仓库**不提供
形式化差分隐私证明**，所有统计实验（如小域 L1 枚举）只是对确定性裁剪界的
一致性检查。

## 参数与定义

`Params`（见 `cbound.py`）：

| 参数 | 含义 | 默认 |
|---|---|---|
| `max_records_per_subject` | 每主体全局最多保留记录数 | 2 |
| `max_groups_per_subject` | 每主体全局最多保留分组数 | 2 |
| `lower` / `upper` | 数值裁剪下/上界（负值同样按界裁剪） | -5.0 / 5.0 |

- **记录**：`(record_id, subject_id, group, value)`，`record_id` 全局稳定唯一。
- **稳定保留策略**（`select_retained`）：组按"组内最小 record_id"排名取前
  `max_groups_per_subject` 个；入选组内按 record_id 字典序取前
  `max_records_per_subject` 条。策略是记录集合的纯函数，与分片顺序、
  记录到达顺序无关——换序结果相同。
- **分片合并**：两状态按 `record_id` 去重取并集后重算同一策略，因此合并
  不绕过全局上限；同一主体分散在两片仍只保留总上限条数；同一 `record_id`
  换分片重复提交会被去重，不能借此绕过上限。
- **撤回 / 更正**：`retract(record_id)` 移除记录、`correct(record)` 替换
  同 ID 记录，随后重算保留集，此前因上限被丢弃的候选自动补回。
- **L1 贡献界**：求和统计量下单主体 L1 上界为
  `max_records_per_subject * max(|lower|, |upper|)`。`check_l1_bound`
  在小合成域上枚举全部主体子集及其相邻集（删一个主体），验证实际最大
  L1 不超过该界。
- **审计**：每次摄入/去重/撤回/更正/合并都写审计日志；
  `clipping_evidence()` 输出每条记录的原始值、裁剪值、是否被裁剪、
  保留状态与丢弃原因；`state_size_bytes()` 输出合并状态的 JSON 序列化大小。

## 文件

- `cbound.py` — 核心库
- `demo.py` — 运行样例：`python3 demo.py`
- `test_cbound.py` — 测试：`python3 -m unittest -v`

## 运行样例（`python3 demo.py`，节选）

```
参数: Params(max_records_per_subject=2, max_groups_per_subject=2, lower=-5.0, upper=5.0)
单主体 L1 求和贡献确定性上界: 10.0

合并事件: {'seq': 1, 'event': 'merge', 'left_records': 4, 'right_records': 3, 'duplicates_removed': 1, 'merged_records': 6}
合并状态大小: 266 字节

== 合并后每条记录的裁剪证据 ==
record  subject  group    raw  clipped  status    drop_reason
r1      s1       g1       3.0      3.0  retained
r2      s1       g1     -99.0     -5.0  retained
r3      s1       g2       4.0      4.0  dropped   超出记录数上限
r4      s2       g1       2.0      2.0  retained
r5      s1       g3       1.0      1.0  dropped   超出分组数上限
r6      s2       g1      88.0      5.0  retained
保留记录: ['r1', 'r2', 'r4', 'r6']

== 撤回 r1 后（候选补回） ==
保留记录: ['r2', 'r3', 'r4', 'r6']

== 小域枚举 L1 检查（非形式化隐私证明） ==
  neighboring_pairs_checked: 12
  actual_max_l1: 2.0
  deterministic_bound: 10.0
  bound_holds: True
```

## 测试结果

```
$ python3 -m unittest 2>&1 | tail -3
Ran 16 tests in 0.001s

OK
```

覆盖：负值/正值按界裁剪、记录数与分组数上限、同主体跨分片仍受全局上限、
记录换序与分片划分/合并顺序无关、跨分片去重、撤回补回、更正重选、
合并状态大小确定性、小域 L1 枚举不越界。

## 免责声明

仅处理合成数据。裁剪只限制贡献幅度；没有噪声机制就没有差分隐私保证，
本仓库的枚举实验不构成也不替代形式化隐私证明。
