# cdep — 带条件的依赖暴露分析器

Python 3.11 标准库实现，读取本地 JSON，不联网。对由有限布尔特性（feature）
加条件的依赖图做符号暴露分析：每个暴露符号给出可达条件、一个满足条件的配置
和对应供应路径；发现冲突时给出共同可行配置和两条独立供应路径；支持边的增/删/
改增量更新。

## 语义

- 边分三类：`public`、`private`、`build`。
- 根自身可见；它能看到**直接** `public` 与 `private` 依赖的导出。
- 继续传递只沿 `public` 边；`private` 边终点之后不再传递（私有传递阻断）。
- `build` 边只进入构建闭包：构建闭包沿 `public`/`build` 传播、不沿
  `private`，且构建闭包中的符号**不**出现在运行暴露中。
- 包可以 `rename`（以别名再暴露一个提供的符号）或 `hide`（隐藏原符号）。
- 符号身份 = `(包ID, 版本, 原符号名)`。同一身份经多条路径到达是合流
  （条件取并），不冲突；**不同身份**在同一可行配置下暴露同名才冲突。
- 条件互斥（合取不可满足）不报冲突。

## 方法（不枚举配置）

- `cdep/bdd.py`：归约有序 BDD（ROBDD，否定用补边语义），`ite`/`and`/`or`/
  `not`/约束代入全部记忆化，固定变量序；同一条件结构共享同一节点。
- `cdep/analyzer.py`：可达性条件是 BDD 节点，最小不动点用 work-list
  增量（`new = old OR incoming`）传播，天然处理依赖环。
- 冲突 = 对两两来源条件做 BDD 合取；可满足才是真冲突，合取的一个模型即共同
  可行配置；路径由在该配置下做 BFS（运行态：至多一条 private 且必须为首条）
  得到。
- 增量删除采用 **delete-and-rederive（DRed）**：先从被删边终点求可能丢失的
  超集并清零（这一步打破环中自支撑残留，引用计数做不到），再由根/私有事实/
  跨边界存活边重新播种，在超集内重取不动点。

## 库用法

```python
from cdep import GraphSpec, Analyzer, Edge, PUBLIC

a = Analyzer(GraphSpec.from_file("cdep/examples/conflict_demo.json"))
a.exposed_names()                 # 暴露符号
a.condition_of("json")            # BDD 条件
a.condition_text(a.condition_of("json"))   # 可读公式
a.sources_of("json")              # {Source: 条件BDD}
w = a.witness_for("json")         # 一个配置 + 供应路径
a.conflicts()                     # [Conflict: common/config/witnesses]
a.build_packages(); a.build_names()        # 构建闭包（不进入运行暴露）

rep = a.add_edge(Edge(PUBLIC, "app@1", "x@2", a.spec.expr.parse("f"), "f"))
rep = a.remove_edge(eid)          # rep.touched_nodes / bdd_nodes_*
rep = a.modify_edge(eid, when="f & !g")
```

JSON 形式见 `cdep/examples/*.json`：边条目为 `"node@version"` 字符串或
`{"to": ..., "when": "<布尔表达式>"}`，支持 `! & | ->` 与括号。

## 演示

```bash
python3 -m cdep.demo query     cdep/examples/dual_route.json
python3 -m cdep.demo conflicts cdep/examples/conflict_demo.json
python3 -m cdep.demo scale
```

## 测试

`cdep/tests/test_analyzer.py`：小图穷举全部配置，与独立的逐配置集合遍历器
（`cdep/oracle.py`，不使用分析器状态）逐配置比对可见包、运行/构建暴露、
冲突；另有 40 幅随机图与 30 轮随机增量序列交叉验证。规定场景全部覆盖：
环入口删除、菱形两路径删一条、别名合流、私有传递阻断、build 闭包不进入运行
暴露、互斥条件不误报。规模用例报告 BDD 节点数（随特性数线性，配置数指数）
与增量触及的图节点数。

```bash
python3 -m unittest discover -s cdep/tests -v
```
