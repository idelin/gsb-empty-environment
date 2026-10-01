# invcheck — 基于共享 BDD 的候选不变量符号验证器

对给定有限状态模型检查候选不变量的**初态成立（基例）**与**一步保持（归纳步）**。
全部判定通过共享布尔决策图（ROBDD）上的符号集合运算完成（交、补、存在量化、
换名、像计算），**默认不枚举任何状态**；仅在小域上另跑一条完全独立的全状态
枚举参考路径用于交叉核验。

- 仅依赖 Python 3.11 标准库；测试使用 `unittest`。
- 验证对象仅限给定的有限状态模型；搜索未完成的结论一律保持 **未决（UNKNOWN）**。

## 功能对照

| 需求 | 实现 |
| --- | --- |
| 基例 / 归纳步符号检查 | `invcheck/checker.py`：`Init ∧ ¬Inv`、`Inv ∧ T ∧ ¬Inv'` 的 BDD 空性判定 |
| 共享 BDD、非逐态枚举 | `invcheck/bdd.py`：唯一表 + ITE/量化/换名缓存 |
| 最小反例 | 对坏集立方体做文字剔除，得到不可约（极小）部分赋值 |
| 可独立检查的证书 | `invcheck/certificate.py`：自包含 JSON，验证方用**全新管理器 + 不同变量序**重算，并用无 BDD 的 AST 语义重放反例 |
| 反例可重放 | `replay`：具体化立方体并逐条求值 `init/inv/T/inv'` |
| 候选增量更正 | `invcheck/incremental.py`：复用管理器缓存的会话、反例制导强化、自动迭代至不动点 |
| 小域全状态参考核验 | `invcheck/reference.py`：独立于 BDD 的 AST 求值枚举，阈值门控 |
| 变量排序不影响结论 | 测试对多种序（含逆序、交错）断言判定一致；证书验证方刻意使用不同序 |
| 资源超额明确未决 | 节点/操作预算超限 → `UNKNOWN` 并给出原因 |
| 未搜索完保持未决 | 可达性 BFS 受迭代上限约束，未收敛时可达性事实全部为 `unknown` |
| 已探索路径成立 ≠ 归纳 | 归纳步失败且反例源不可达时明确说明"在全部可达状态上成立但不归纳" |
| 空初态单独报告 | `init_empty` 标志 + 专门说明（基例 vacuous 成立） |

## 模型格式（JSON）

```json
{
  "name": "mod3",
  "variables": ["b1", "b0"],
  "init": "~b1 & ~b0",
  "invariant": "~(b1 & b0)",
  "transition": {"edges": [[0, 1], [1, 2], [2, 0], [3, 3]]}
}
```

变迁关系三种等价形式：`edges`（整数编码边表，变量 MSB 在前）、
`next`（每位的次态函数，未列出者保持原值）、`relation`（含 `x'` 的关系式）。
表达式支持 `~ ! not`、`& and`、`| or`、`^ xor`、`->`、`<->`、`0/1/true/false`。

## 运行样例

### 1. 检查并签发证书（归纳步反例源不可达的情形）

```
$ python3 -m invcheck check examples/mod5_unreachable_step.json --cert /tmp/mod5_cert.json --reference
verdict: FAIL_STEP
  invariant:      ~(b2 & ~b1 & b0)
  init empty:     False
  base case:      ok
  inductive step: FAILED
  step cex src:   b1 & b2
  step cex dst:   b0 & ~b1 & b2
  cex source is:  unreachable
  reachable:      5 states (complete)
  holds on reachable: True
  note: inductive step failed ONLY through unreachable states: the candidate may
        still hold on every reachable state, but it is not inductive; holding on
        explored paths is not induction -- strengthen the candidate
  note: the candidate holds on ALL reachable states (confirmed by complete
        symbolic reachability), yet it is not inductive
  stats: {'bdd_nodes': 140, 'bdd_ops': 210, 'variables': 6}
reference cross-check: agrees with exhaustive enumeration
certificate written to /tmp/mod5_cert.json
```

（mod-5 计数器：候选"状态 ≠ 5"在全部可达状态 0..4 上成立，但不可达状态
6/7 满足候选却一步进入 5 —— 不可达状态导致归纳步失败，工具明确说明。）

### 2. 独立验证证书（验证方使用不同变量序重算）

```
$ python3 -m invcheck verify /tmp/mod5_cert.json
[ok ] model_digest -- sha256 of canonical model encoding
[ok ] fresh_variable_order -- verifier uses its own variable order: ["b0'", 'b0', "b1'", 'b1', "b2'", 'b2']
[ok ] verdict -- recomputed=FAIL_STEP certified=FAIL_STEP
[ok ] init_empty
[ok ] base_ok
[ok ] step_ok
[ok ] reachable_states -- recomputed=5 certified=5
[ok ] holds_on_reachable
[ok ] step_cex_valid -- cube is nonempty and contained in the bad set
[ok ] step_cex_minimal -- dropping any literal leaves the bad set (irredundant)
[ok ] replay -- replay step cex: 110 -> 101 invariant(s)=True transition=True invariant(s')=False
certificate VALID
```

### 3. 重放反例

```
$ python3 -m invcheck replay /tmp/mod5_cert.json
replay step cex: 110 -> 101 invariant(s)=True transition=True invariant(s')=False
```

### 4. 增量更正候选（自动强化至归纳）

```
$ python3 -m invcheck strengthen examples/mod5_unreachable_step.json
round 0: ~(b2 & ~b1 & b0)                         -> FAIL_STEP
round 1: (~(b2 & ~b1 & b0)) & ~(b1 & b2)          -> PASS
final candidate: (~(b2 & ~b1 & b0)) & ~(b1 & b2)
final verdict:   PASS
```

### 5. 资源超额 → 明确未决

```
$ python3 -m invcheck check examples/mod5_unreachable_step.json --max-nodes 4
verdict: UNKNOWN
  base case:      undecided
  inductive step: undecided
  undecided:      model construction exceeded resources: node budget 4 exceeded
  note: resource budget exceeded; verdict left undecided
```

### 6. 空初态单独报告

```
$ python3 -m invcheck check examples/empty_init.json
verdict: PASS
  init empty:     True
  note: initial state set is EMPTY; the base case holds vacuously and is reported separately
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...
Ran 53 tests in 0.4s

OK
```

覆盖：BDD 核心（连接词真值表、ITE、量化、换名、sat_count、共享唯一表、
节点/操作预算）、表达式解析、六种判定情形（归纳通过 / 基例失败 / 归纳步
失败且源可达 / 源不可达 / 空初态 / 零变量）、5 种变量序下结论一致、
反例立方体不可约性、证书验证与篡改检测（判定、反例、摘要）、重放、
参考核验与全部示例的符号结果交叉一致、增量强化收敛与 FAIL_BASE 终止。

## 目录结构

```
invcheck/
  bdd.py          # 共享 ROBDD 管理器（唯一表、ITE、量化、换名、资源预算）
  expr.py         # 布尔表达式解析 / AST 求值（独立于 BDD 的语义路径）
  model.py        # 有限状态模型与三种变迁形式
  checker.py      # 基例 / 归纳步符号检查、可达性、判定与说明
  certificate.py  # 证书生成、独立验证（换序重算）、反例重放
  reference.py    # 小域全状态枚举参考核验（唯一的枚举路径，阈值门控）
  incremental.py  # 候选增量更正会话与自动强化
  cli.py          # check / verify / replay / strengthen / demo
examples/         # 六个示例模型
tests/            # 53 个 unittest 用例
```
