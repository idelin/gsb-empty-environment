# proofnet — 超图证明网络

Python 3.11+，仅标准库，测试用 unittest。

## 语义

- **AND 超边**：`{A,B}→C` 要求 A、B 同时成立才推出 C；任一前置成立不够。
- **替代证明**：同一结论可挂多条超边，任一边全部前置成立即维持结论；
  撤回其中一条路径（如撤回 B）后，结论可由另一条路径（如 `D→C`）维持。
- **循环需种子**：推导取最小不动点，纯循环 `C→E→C` 无外部种子时均不成立。
- **证明成本**：结论成本 = 边成本 + 各前置最小成本之和，跨替代证明取最小；
  不可证为 `inf`。成本须非负。
- **字符串 ID**：顶点、超边、种子均为字符串 ID；孤立顶点保留；允许平行多重边。
- **时间区间**：左闭右开 `[start, end)`，默认始终有效；种子与超边各自带区间。

## API 摘要

```python
from proofnet import Interval, ProofNetwork

net = ProofNetwork()
net.add_vertex("X")                                  # 孤立顶点
net.add_edge("e1", ("A", "B"), "C", cost=2.0)        # AND 超边
net.add_seed("sA", "A", Interval(0, 10))             # 外部种子，[0, 10)
net.holds("C", t=3)          # 是否成立
net.proof_cost("C", t=3)     # 最小证明成本（不可证为 inf）
net.proof("C", t=3)          # 最小成本证明树
net.derived(t=3)             # t 时刻全部成立顶点
net.retract_seed("sA")       # 撤回种子
net.retract_edge("e1")       # 撤回超边
```

## 运行

```bash
python3 demo.py                 # 运行样例
python3 -m unittest test_proofnet -v   # 运行测试（21 个）
```
