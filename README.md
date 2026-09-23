# pareto_path

预算约束下的双目标(成本/时延)帕累托最优简单路径求解, Python 3.11, 仅标准库。

## 模型

- 有向多重图, 顶点与边均为字符串 ID, 保留孤立点与多重边。
- 边含非负 `cost` / `delay` 与可用时间区间 `[start, end)`(左闭右开), 到达后可等待。
- 路径不重复顶点; 总成本 `<= budget`(等于预算允许); 无可行路返回空集。
- 输出按成本升序的帕累托前沿, 被两维同时支配的路径不输出。
- `max_results` / `max_labels` 为明确上限; 触发时 `truncated=True` 且 `complete=False`,
  截断结果不得称为完整。

## 用法

```python
from pareto_path import Edge, Graph, pareto_frontier

g = Graph()
g.add_vertex("iso")  # 孤立点保留
g.add_edge(Edge("e1", "s", "a", cost=1, delay=4, start=0, end=10))
g.add_edge(Edge("e2", "a", "t", cost=0, delay=5))
g.add_edge(Edge("e3", "s", "t", cost=5, delay=2))

result = pareto_frontier(g, "s", "t", budget=5, max_results=100)
for e in result.entries:
    print(e.cost, e.delay, e.vertices, e.edges)
assert result.complete  # 截断时为 False
```

## 运行

```sh
python3.11 pareto_path.py        # 运行样例
python3.11 -m unittest -v        # 运行测试
```
