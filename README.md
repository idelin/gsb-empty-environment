# 等价类多样性指标与最少抑制（纯标准库，Python 3.11）

按等价类计算 **不同敏感值数** 与 **熵** 两种指标，支持显式 null 策略、
失败类及各项贡献输出、增量删除更新、层级合并与撤回、熵阈值附近的
区间三值判定、独立重算验证，以及达到约束的最少抑制记录搜索。

> **范围声明**：本项目只处理合成数据（`synthetic.py` 生成）。
> 所有输出为统计实验结果，**不作为形式隐私证明**。

## 定义

- **等价类**：准标识符取值相同的一组记录，以 `class_key` 标识。
- **不同敏感值数（distinct）**：类内敏感值去重后的类别数。
  同一值出现 10 次仍只算 1 种。
- **熵（entropy）**：`H = -Σ p_i · log_base(p_i)`，其中 `p_i = count_i / n`，
  对数底由 `Config.log_base` 指定（如 2、e、10）。
- **贡献（contribution）**：值 `i` 的份额 `p_i` 与熵项 `-p_i · log_base(p_i)`，
  全部值的熵项之和等于类熵。

## 参数（`metrics.Config`）

| 参数 | 含义 | 默认 |
| --- | --- | --- |
| `distinct_threshold` | 不同敏感值数下限 L | 1 |
| `entropy_threshold` | 熵下限 E | 0.0 |
| `log_base` | 熵的对数底（>1） | 2.0 |
| `null_policy` | `IGNORE`（null 不入统计）或 `COUNT_AS_CATEGORY`（null 算一个类别） | `IGNORE` |
| `count_all_null_class` | 全 null 类是否仍计入评估（否则状态为 `skipped`） | `True` |

## 判定规则

- 每个类输出 `pass` / `fail` / `undecided` / `skipped` 及未达标项
  （`distinct`、`entropy`），失败类附带各敏感值的贡献明细。
- **熵的区间判定**：熵以向外舍入的浮点区间计算，误差界随运算步数
  可加累积（`intervals.Interval`，log 求值误差按固定 ulp 外扩的工程界）。
  区间整体 ≥ 阈值 → 满足；整体 < 阈值 → 违反；阈值落入区间
  （浮点精度无法区分）→ **未决（undecided）**。

## 功能与语义

- **增量删除**：`ClassStats.delete()` 只更新计数器，不重扫原始记录；
  `verify(records)` 用原始记录独立重算并比对（覆盖重复值、null）。
- **分片合并**：`merge_shard()` 计数可加；测试验证合并结果与整体重算一致。
- **层级合并与撤回**：`Hierarchy` 给出 child→parent 映射（解析到根），
  `merge_values()` 合并时把原贡献存入撤销日志，`rollback()` 精确恢复。
- **最少抑制**：抑制 = 删除整条记录；空类视为自然满足；各类相互独立，
  全局最少抑制数 = 各类之和。`min_suppression_exact` 按删除总数递增枚举
  （仅适合小域），`min_suppression_greedy` 每步删除计数最大的值；
  熵未决按不满足处理（保守）。测试用 `itertools.product` 独立枚举校验精确解。

## 运行

```bash
python3.11 demo.py                                    # 运行样例
python3.11 -m unittest discover -s tests -t . -v      # 运行测试
```

## 文件

- `intervals.py` — 向外舍入区间运算与三值判定
- `metrics.py` — 配置、等价类统计、评估报告、独立重算验证
- `hierarchy.py` — 敏感值层级映射
- `suppression.py` — 可行性判定、精确/贪心最少抑制
- `synthetic.py` — 合成数据生成（含重复值与 null）
- `demo.py` — 运行样例
- `tests/` — unittest 测试（29 个用例）
