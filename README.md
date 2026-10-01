# 请求-响应活性检查器（有限状态模型）

对给定有限状态模型验证性质 **“请求后最终响应”**，即 LTL 公式
`G(req -> F resp)`：一旦进入带 `req` 标签的状态，最终必须到达带
`resp` 标签的状态。仅使用 Python 3.11 标准库，测试基于 unittest。

## 语义与判定

- **不履行循环**：搜索空间是增广图 `(state, pending)`，`pending`
  表示请求尚未应答（进入 `resp` 状态清除，进入 `req` 状态置位）。
  全由 `pending=True` 节点构成的可达环即为反例（lasso：前缀 + 环）。
  **自循环同样构成反例**（环长为 1）。
- **公平性只按显式动作集合定义**：`fairness_sets=[{"respond"}, ...]`
  对每个集合施加弱公平——若某集合在环上每个状态都可发生却从未被
  采取，该环被排除。未列出的动作不做任何公平假设。被公平性排除的
  环记录在证据 `ignored_unfair_components` 中，**公平假设的改变必然
  体现在证据里**（`fairness_sets` 与被忽略环列表）。
- **未搜索完保持未决**：`max_nodes` 限制增广节点数。截断时若未找到
  反例，判定为 `PENDING` 而非违反——有限截断未见响应不能直接判违反；
  只有完整的环证明 `VIOLATED`，只有完备的搜索证明 `SATISFIED`。
- **反例可重放**：反例是 `prefix + loop` 的动作/状态序列，
  `replay()` 在模型上逐步重放并校验：每步是真实迁移、pending 标志
  一致、环闭合、环上请求始终未应答、环上无 `resp` 状态，且满足所
  声明的公平集合。

## 文件

- `fsm.py` — 有限状态模型（状态标签 + 动作迁移）。
- `checker.py` — `check()` 判定器、`replay()` 重放器、证据结构。
- `examples.py` — 三个示例模型与六个演示场景。
- `test_checker.py` — 14 个 unittest 用例。

## 运行

```sh
python3.11 examples.py        # 运行样例
python3.11 -m unittest -v     # 运行测试
```

## 运行样例（节选）

```
== lossy service, no fairness assumptions
verdict: violated
counterexample:
  prefix --request--> wait (pending=True)
  loop   --spin--> wait (pending=True)
replay: OK (8 checks)

== lossy service, weak fairness on {respond}
verdict: satisfied
fairness sets declared: [['respond']]
ignored unfair cycle over states ['wait']; it never takes and always enables [['respond']]

== lossy service, weak fairness on {spin} only (respond gets no assumption)
verdict: violated            # 只对显式列出的集合施加公平性

== deep service, search truncated at 4 nodes
verdict: pending             # 截断未见响应 -> 未决，而非违反
explored: 5 nodes, 4 edges, truncated=True

== deep service, full search
verdict: violated            # 搜索完备后给出带前缀的反例
```

## 测试结果

```
Ran 14 tests in 0.001s
OK
```

覆盖：满足/违反/未决三种判定、自循环反例、截断不判违反、完备搜索
解决未决、公平性排除自循环、未列出动作不做公平假设、公平假设记录
在证据中、公平环仍判违反、反例重放通过、篡改动作/pending 标志被
重放拒绝、重放强制执行的公平假设。
