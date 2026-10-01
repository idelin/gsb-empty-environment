# 有限域反例收缩器（shrinker）

给定一个整数向量和一个确定性失败谓词，在只允许两种操作的前提下把反例收缩到最小：

- **删除**任意一个元素；
- **减小**任意元素的绝对值（向 0 靠近一步，**不允许变号**：正数最小到 0，负数最大到 0）。

目标按字典序最小化键 `(长度, 绝对值之和, 向量字典序)`。

因为 `[-1,-1]` 从 `[4,4,9]` 不可达（4 无法变号为负），所以谓词「存在连续两个相同非零数」下
`[4,4,9]` 的最小反例是 **`[1,1]`**，而不是 `[-1,-1]`。

## 正确性保证

- 每种操作都严格减小目标键，可达空间是以键排序的有限 DAG。
- 搜索按键的 best-first 顺序弹出状态；**每个弹出的状态都会被扩展**（生成一步邻居），
  因此每个可达状态最终都会被生成。谓词评估只花在键严格小于当前最优失败状态的向量上。
- 仅当优先队列清空——即所有键小于结果的可达向量都已生成并测试——才输出
  `"proven_minimal": true`。**绝不会把局部极小冒充全局最小**。
- 谓词预算或扩展上限（`--max-expansions`）耗尽时，输出 `"proven_minimal": false`，
  并在 `unexplored_frontier` 中列出已生成但未测试、且键小于当前结果的边界状态；
  `frontier_complete` 为 `false` 表示边界之外还可能有未探索区域。
- 所有谓词调用结果都缓存在内存中，同一向量绝不重复评估（`predicate_calls == cache_size`）。

## 运行环境

Python 3.11+，仅使用标准库，无第三方依赖。

## 命令行用法

```bash
# 方式一：--vector 直接给 JSON 数组
python3 shrinker.py --predicate consecutive_equal_nonzero --vector '[4,4,9]'

# 方式二：stdin / --input 文件，内容为 JSON 数组或对象
echo '[4,4,9]' | python3 shrinker.py --predicate consecutive_equal_nonzero
echo '{"vector": [4,4,9], "predicate": "consecutive_equal_nonzero", "budget": 1000}' \
  | python3 shrinker.py
```

参数：

| 参数 | 含义 | 默认值 |
|---|---|---|
| `--predicate` | 内置谓词名（见下） | 必填（或输入对象的 `predicate` 字段） |
| `--budget` | 谓词真实评估次数上限 | 10000 |
| `--max-expansions` | 状态扩展次数上限（防爆内存） | 1000000 |
| `--vector` | JSON 数组形式的输入向量 | — |
| `--input` | JSON 输入文件；省略或 `-` 表示 stdin | stdin |

命令行参数优先于输入对象中的同名字段。输入向量必须是整数数组。

内置谓词（确定性）：

- `consecutive_equal_nonzero`：存在连续两个相同且非零的元素；
- `any_negative`：存在负元素；
- `sum_abs_at_least_10`：绝对值之和 ≥ 10。

## 输出格式（stdout，JSON）

| 字段 | 含义 |
|---|---|
| `input` | 原始输入向量 |
| `input_fails` | 输入是否满足失败谓词（预算为 0 时为 `null`，表示未验证） |
| `result` | 收缩结果（未证最优时是预算内找到的最优失败向量） |
| `result_key` | 结果的目标键 `[长度, 绝对值和, 向量]` |
| `proven_minimal` | 是否已证明为可达空间内的全局最小 |
| `budget` / `budget_exhausted` | 预算额度 / 是否因预算耗尽而停止 |
| `expansion_capped` | 是否因扩展上限而停止 |
| `predicate_calls` / `cache_hits` / `cache_size` | 真实谓词调用数 / 缓存命中数 / 缓存条目数 |
| `expansions` / `states_generated` | 扩展次数 / 生成的不同状态数 |
| `unexplored_frontier` | 未探索边界（键小于结果但未测试的向量，最多列 100 条） |
| `unexplored_frontier_total` | 边界状态总数 |
| `frontier_complete` | 边界是否完整（`false` 表示之外可能还有未生成的状态） |
| `notes` | 人类可读的结论说明（未证最优或未验证时会明确写出） |
| `predicate` | 使用的谓词名 |

## 测试

```bash
python3 -m unittest test_shrinker -v
```

覆盖：验收边界 `[4,4,9] → [1,1]`、符号不可翻转、全局最小 vs 局部极小、
删除至空向量、字典序平局、缓存去重、预算耗尽 / 零预算 / 扩展上限、
非失败输入、CLI 端到端。所有示例数据均为合成数据。
