# 含容量上下界的最小费用整数流

纯 Python 3.11 标准库实现，不调用外部求解器。节点供需以正 `supply` 和负 `supply` 表示，边为有向边，包含整数或精确有理数下界、上界和单位费用。

## 文件

- `bounded_mincost_flow.py`：核心数据结构与算法。
- `demo.py`：最优流、负费用循环、预算未决和不可行割证据样例。
- `test_bounded_mincost_flow.py`：15 个 `unittest` 测试。

## 算法

1. **检查总供需**：正数供应之和必须等于负数需求绝对值之和，否则返回 `INFEASIBLE`。
2. **整数投影**：边流量必须为整数，因此对每条边取
   - 可占用最小整数流量 `L = ceil(lower)`；
   - 最大整数流量 `U = floor(upper)`；
   - `L > U` 时该边没有可行整数流量。
3. **下界先占用流量**：初始令 `f(e)=L(e)`，并更新节点平衡：
   - 尾节点减少 `L`；
   - 头节点增加 `L`。
4. **构造可行流**：在剩余容量 `U-L` 的残量网络上加入超级源和超级汇，用手写 Dinic 最大流满足调整后的节点平衡。
5. **不可行割证据**：若超级源到超级汇的最大流不足，输出超级源可达割集 `X`，以及
   - 净流出上界：`Σ U(out of X) - Σ L(into X)`；
   - 所需净流出：`Σ adjusted_balance(v), v ∈ X`；
   - 缺口：所需净流出减净流出上界。
6. **最小费用优化**：在可行流的残量网络中用 Bellman-Ford 检测负费用有向环，每次沿环增加整数瓶颈流量。负环按其残量容量有界抵消；自环和平行边也支持。
7. **状态分离**：
   - `OPTIMAL`：找不到负环，残量网络负环证书为空；
   - `FEASIBLE`：`prove_optimal=False`，只构造可行流；
   - `BUDGET_UNDECIDED`：达到 `max_cycles` 后仍检测到负环，并返回该环作为未决证据；
   - `INFEASIBLE`：供需不等、无整数边界值、分数节点平衡或割证据不足。

费用和输入用 `fractions.Fraction` 精确运算；容量增量和最终流量均为 `int`。

## 用法

```python
from bounded_mincost_flow import Edge, FlowProblem

problem = FlowProblem(
    [Edge("s", "t", lower=0, upper=2, cost=1, key="st")],
    {"s": 2, "t": -2},
)
result = problem.solve()
print(result.status, result.flow, result.cost)
```

运行样例：

```bash
python3.11 demo.py
```

运行测试：

```bash
python3.11 -m unittest -v
```

`max_cycles=None` 可取消迭代次数限制；由于容量有界且每次整数增广都会严格降低费用，算法会终止。

## 样例输出

```text
== 最优整数流 ==
status: optimal
flow: {'cheap-in': 2, 'other-in': 0, 'cheap-out': 2, 'other-out': 0}
cost: 4
cycles_canceled: 0

== 可达负费用循环 ==
status: optimal
flow: {'shipment': 1, 'discount': 2, 'return': 2}
cost: 2
cycles_canceled: 1

== 预算未决 ==
status: budget_undecided
flow: {'negative-self-loop': 0}
cost: 0
cycles_canceled: 0
remaining_negative_cycle: a -> a

== 不可行证据 ==
status: infeasible
flow: {}
cost: 0
cycles_canceled: 0
evidence: cut_certificate - no feasible flow satisfies lower bounds and node balances
reachable: ('s',)
net_outflow_bound: 1
required: 2
deficit: 1
```

## 测试结果

```text
Ran 15 tests in 0.002s

OK
```
