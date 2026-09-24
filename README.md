# hyperproof —— 超图证明系统

Python 3.11+, 仅标准库。超边为 AND 语义: 所有前置节点成立才产生结论。

## 语义

- **AND 超边**: `{A,B}→C` 只有 `A` 不能推 `C`; 绝不退化为 "任一前置即可"。
- **替代证明**: 同一结论可有多条超边, 任一证明链成立即成立 (如另有 `D→C`, 撤回 `B` 后 `C` 仍由 `D` 维持)。
- **撤回**: `retract_seed` 移除外部种子, 派生结论按最小不动点重算并级联失效。
- **证明成本**: 节点成本 = min over 证明方案 of (超边成本 + 各前置最小成本之和), 种子为 0; Bellman-Ford 式松弛, `|V|` 轮收敛。
- **循环需种子**: 推导取最小不动点, 纯 `C→E→C` 无种子时均不成立。
- **字符串 ID**: 顶点与超边均用字符串 ID; 保留孤立点; 允许同一 (前置, 结论) 的多重边 (边 ID 唯一)。
- **时间区间**: 种子按左闭右开区间 `[start, end)` 断言, 查询按时刻 `t` 进行。

## 用法

```python
from hyperproof import HyperProofGraph

g = HyperProofGraph()
g.add_vertex("isolated")                    # 孤立点
g.add_edge("e1", ("A", "B"), "C", cost=2.0) # {A,B}->C
g.add_edge("e2", ("D",), "C", cost=5.0)     # 替代证明 D->C
g.assert_seed("A", 0.0, 10.0)               # 种子在 [0, 10) 有效
g.assert_seed("B")
g.assert_seed("D")

g.holds("C", t=1.0)      # True
g.proof_cost("C", 1.0)   # 2.0 (取最小成本证明)
g.retract_seed("B")      # 撤回 B, C 仍由 D 维持
```

## 运行

```sh
python3 demo.py                    # 运行样例
python3 -m unittest test_hyperproof -v   # 运行测试 (21 个)
```

## 文件

- `hyperproof.py` —— 核心模块 (`HyperProofGraph`, `Interval`, `Hyperedge`, `ProofNode`)
- `test_hyperproof.py` —— unittest 测试套件
- `demo.py` —— 运行样例
