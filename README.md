# invcheck — 有限状态模型的符号化不变量检查器

对用户给定的**候选不变量**检查两条证明义务，并输出基例或归纳步反例：

- **基例（base case）**：`init ⊆ inv`，即所有初态满足不变量；
- **归纳步（one-step preservation）**：`inv ∧ T ⇒ inv'`，即不变量被迁移关系保持。

候选不变量与状态集合统一表示为**共享 ROBDD**（单一唯一表 + 缓存，见
`invcheck/bdd.py`）。初态检查与一步保持检查全部通过**符号集合运算**
（交、补、存在量化、换名、限制）完成，**默认不枚举任何状态**；具体状态仅在
提取反例见证时物化。纯 Python 3.11 标准库实现，测试基于 `unittest`。

## 设计要点

- **最小反例**：取当前变量序下字典序最小（`false < true`）的见证，确定性可复现。
  归纳步反例为状态对 `(s, s')`：`inv(s)` 成立、`s → s'` 是模型迁移、`inv(s')` 被违反。
- **可重放证书**：`--cert` 生成自包含 JSON 证书（内嵌模型及其 SHA-256、候选、
  判定、反例）。`replay` 子命令只用**具体表达式求值**独立复核，完全不经过 BDD 引擎。
- **已探索 ≠ 归纳**：检查器额外用符号不动点计算精确可达集。若归纳步反例的前提
  状态不可达，会明确说明"候选在所有可达状态上成立但不是归纳不变量"，需加强候选。
- **空初态单独报告**：`base_status = empty_init`（基例空虚成立），与 `pass` 区分。
- **变量序无关性**：判定结论（verdict / base / inductive / 可达计数）不随变量序
  变化；仅最小见证的具体形态可能不同（测试覆盖 interleave/block × 多种排列）。
- **资源超额即未决**：BDD 节点上限（`--node-limit`）或时限（`--time-limit`）被
  突破时，受影响的检查报告 `undecided`，绝不静默判成通过/失败；枚举参考器超出
  `--max-states` 同样保持未决。
- **增量更正**：候选文件支持 `refine:` 行逐级合取加强，模型编码与 BDD 缓存跨
  阶段复用（`InvariantChecker` / `check_incremental`）。
- **参考核验**：`--reference` 或 `enumerate` 子命令对小域做全状态枚举并与符号
  结果比对；测试另含 40 个随机模型的符号/枚举交叉验证。

## 模型格式（JSON）

```json
{
  "name": "mod4_counter",
  "variables": ["a", "b"],
  "init": "!a & !b",
  "transitions": [
    {"name": "wrap", "guard": "a & b", "assign": {"a": "false", "b": "false"}},
    {"name": "tick", "guard": "!(a & b)", "assign": {"b": "!b", "a": "a ^ b"}}
  ]
}
```

未赋值的变量保持原值（框架条件）；多个迁移可同时使能（非确定性）。表达式
运算符：`! ~ not`、`& and`、`^ xor`、`| or`、`-> =>`、`<-> <=>`、括号、
`true/false/0/1`。

## 候选文件格式

```
invariant: !a
refine: !b        # 第 2 阶段检查 (!a) & (!b)，可多条逐级加强
```

## 运行样例

```console
$ python3 -m invcheck check examples/mod4_counter.json --inv examples/inv_no3.txt --cert /tmp/cert.json
== stage 1: candidate !(a & b) ==
  verdict:           inductive_failed
  base case:         pass
  inductive step:    fail
    counterexample (lexicographically minimal under the variable order):
      premise   s  = {a=1, b=0}   (invariant holds)
      successor s' = {a=1, b=1}   (invariant violated)
      premise reachable from init: yes
  reachable states:  4; invariant holds on all reachable states: False
  bdd nodes: 43

$ python3 -m invcheck replay /tmp/cert.json
  [ok] model hash matches embedded model
  [ok] inductive: inv(s) holds
  [ok] inductive: s -> s' is a model transition
  [ok] inductive: inv(s') is violated
replay succeeded (certificate verdict: inductive_failed)
```

不可达前提 + 增量加强（latch 例子）：

```console
$ python3 -m invcheck check examples/latch.json --inv examples/inv_latch.txt
== stage 1: candidate !a ==
  verdict:           inductive_failed
  ...
      premise reachable from init: NO (unreachable state)
  note: inductive counterexample starts from an UNREACHABLE state: the candidate
        holds on every reachable state but is not inductive; holding on explored
        paths does not imply inductiveness -- strengthen the candidate
== stage 2: candidate (!a) & (!b) ==
  verdict:           verified
```

其余情形：

```console
$ python3 -m invcheck check examples/mod3_counter.json --inv examples/inv_mod3.txt --reference
  verdict: verified ... reference enumeration: agrees (full state space)
$ python3 -m invcheck check examples/empty_init.json --inv examples/inv_mod3.txt
  base case: empty_init   # 空初态单独报告，基例空虚成立
$ python3 -m invcheck check examples/mod4_counter.json --inv examples/inv_no3.txt --node-limit 10
  verdict: undecided      # 资源超额明确未决，exit code 2
$ python3 -m invcheck check examples/mod4_counter.json --inv examples/inv_no3.txt --order block --permute random:5
  verdict: inductive_failed   # 变量序变化不影响结论
```

退出码：`0` = verified，`1` = 找到反例，`2` = undecided / 错误。

## 测试结果

```console
$ python3 -m unittest discover -s tests -v
...
Ran 45 tests in 0.110s

OK
```

覆盖：BDD 语义 vs 随机表达式真值表、量化/换名/限制/计数、最小见证的字典序
性质、节点/时限超额；mod3/mod4/latch/空初态各判定路径；不可达前提说明；
增量加强；变量序不变性（2 种风格 × 随机排列）；证书生成/落盘/重放/篡改
检测；40 个随机模型符号结果与全枚举参考的一致性；CLI 端到端。

## 目录结构

```
invcheck/
  bdd.py         # 共享 ROBDD 管理器（唯一表 + 缓存 + 资源预算）
  expr.py        # 布尔表达式解析 / 具体求值 / 编译为 BDD
  model.py       # 有限状态模型加载与具体语义
  checker.py     # 符号化基例/归纳步检查、可达集、反例提取、增量加强
  refcheck.py    # 小域全枚举参考核验（仅供交叉验证）
  certificate.py # 证书生成与独立重放
  cli.py         # 命令行：check / replay / enumerate
examples/        # mod3/mod4 计数器、latch（不可达前提）、空初态
tests/           # unittest 测试套件
```
