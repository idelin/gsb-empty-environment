# 对账键冲突解释器 (Reconciliation Key-Conflict Interpreter)

纯 Python 3.11 标准库实现，无第三方依赖。所有示例数据（`examples/`）均为合成数据。

## 运行

```bash
# 一对一匹配
python3.11 -m recon onetoone --left examples/left.json --right examples/right.json --window 60

# 拆并账匹配（一对多 / 多对一，精确集合打包）
python3.11 -m recon splitmerge --left examples/left.json --right examples/right.json \
    --window 60 --verify-exhaustive [--budget N] [--no-explain]
```

`--output FILE` 可将报告写入文件，默认输出到 stdout。

## 输入格式

左右两张交易表各为一个 JSON 文件，顶层为数组，每行：

```json
{"id": "a", "keys": ["inv-100", "inv-200"], "amount": "10.00", "time": 1000}
```

- `id`：行标识（表内唯一）
- `keys`：候选业务键集合
- `amount`：金额（建议字符串以避免浮点误差，内部按 `Decimal` 精确处理）
- `time`：时间戳（epoch 秒）

`--window` 为允许的最大时差（秒）。

## 模式一：onetoone（一对一）

**可行性**：左右两行共享至少一个候选业务键、金额严格相等、时差不超过窗口；每行最多使用一次。

**目标（字典序）**：① 最大化匹配对数 → ② 最小化总时差 → ③ 配对 ID（`"<left>::<right>"`）排序后字典序最小。

**实现**：最大基数由最大流保证；目标②③编码为精确的元组费用 `(时差, 2^E − 2^(E−1−rank))`，
用带势函数的连续最短路求解。2 的幂权在等基数解之间与"排序后字典序"严格等价
（最高不同位支配所有低位之和）。

**未匹配解释**：每行标注 `no_candidate`（无任何兼容对手行）或 `competition`
（有候选但被全局竞争挤出，附交错可达性证据）。

**最优性证据**（`verification` 字段）：
- `vertex_cover_size == matching_size`：Kőnig 最小点覆盖证书，证明基数最大；
- `augmenting_path_free`：从未匹配左行出发的交错 BFS 到不了未匹配右行，即无增广路径；
- `residual_negative_cycle_found == false`：残余网络无负费用环，证明费用（目标②③）最优。

## 模式二：splitmerge（拆并账）

**不是**普通二分图匹配：可行单元是最多 3 行的"组"，问题按精确集合打包求解。

**组可行性**：2–3 行且两侧至少各一行；全组共享至少一个业务键（键集交集非空）；
组内左侧金额总和与右侧金额总和严格相等；组内时间跨度（max−min）不超过窗口。
每行最多出现在一个配对组。

**目标（字典序）**：① 最大化匹配金额绝对值总和 → ② 最大化匹配行数 →
③ 最小化组内最大时差（跨度）总和 → ④ 规范组 ID（`"L1+L2=R1"` 形式）排序后字典序最小。
目标④同样用 2 的幂权精确编码（在前两目标并列类中不存在组集合的真包含关系，故等价）。

**精确搜索与验证**：
- 分支限界精确搜索（`exact_search`），上界剪枝保证精确性；
- `--budget N`：节点预算。耗尽时输出当前最优解作为**下界**、松分量上界，
  并置 `optimal=false`（明确标注"未证最优"）；
- `--verify-exhaustive`：用无剪枝的独立穷举枚举对照目标值，结果写入
  `search.exhaustive_check`。

**未匹配行分析**（`forced_inclusion`）：对每个未匹配行，强制纳入后重新求最优：
- `infeasible`：该行不属于任何可行组，完全不可行；
- `equal_alternative`：存在包含该行且前三个实质目标同优的解（第④项目标仅为决胜规则）；
- `loss`：强制纳入会严格损失，报告各目标分量的损失值。

## 输出格式

JSON 报告，主要字段：
- `objectives` / `objective`：各级目标取值；
- `pairs` / `groups`：配对结果（含共享键、时差/跨度）；
- `unmatched`：未匹配行及原因/强制纳入分析；
- `verification`（onetoone）或 `search`（splitmerge）：最优性证据；
- `optimal`：是否已证明最优。预算中断时为 `false` 并附上下界。

## 测试

```bash
python3.11 -m unittest discover -s tests -v
```

覆盖：验收边界（左 a 候选 {x,y}、左 b 只候选 x，最优必须 a-y、b-x，并演示贪心 a→x 会漏掉 b）、
目标字典序、约束边界、未匹配分类、拆并账组可行性、预算中断上下界、
分支限界与独立穷举的随机对照（30 个种子实例）、CLI 端到端。
