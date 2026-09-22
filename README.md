# statree — 带状态约束的有序树自动机

纯 Python 3.11 标准库实现（无正则、无第三方自动机库），在**共享子树 DAG**
上做自底向上的状态推导，显式拒绝结构环，支持局部编辑的增量维护、快照、
路径敏感证据与资源控制，全部用 `unittest` 验证。

## 运行

```bash
python3.11 run_tests.py                       # 单元测试 + 小树穷举对照
PYTHONPATH=src python3.11 examples/demo.py   # 可重放端到端样例
PYTHONPATH=src python3.11 examples/counterexamples.py  # 可独立执行的反例
```

无需安装依赖；也可 `pip install -e .` 后 `import statree`。

## 自动机模型

- 字母表显式输入；节点为有序、带属性映射的不可变 DAG 节点
  （`statree.dag.Node`：标签 + 有序子序列 + 属性）。
- 规则 `q: f(S1..Sn) when cond`（`statree.model.Rule`）在节点
  **标签相同、元数相同、第 i 个孩子属于 Si、属性条件成立** 时触发并赋状态
  `q`；空元规则（n=0）是叶子基例。同一节点可有多个状态。
- 接受集合由 `Signature`（可按标签/元数过滤）与根节点状态相交得到。
- 元数就是孩子个数，规则按 `(标签, 元数)` 建索引；同标签不同元数的规则
  永不混用。

### DAG：共享一次、结构环禁止

- 节点内容寻址（hash-consing）：结构相同的子树只存一份、只推导一次。
- `Dag.from_adjacency` 显式接收 `(id,label,children[,attrs])`，构造时
  DFS 检测有向环/自环，报出具体环路证据；悬空引用同样拒绝。

### 最小不动点语义：无根支撑不能自成立

`statree.engine.least_fixpoint` 从**空状态赋值**出发做同步轮次：每一轮对
所有节点用轮初快照触发规则，直到稳定。因此只有存在**有限证明树**的状态
才会出现；纯规则循环（如 `qY :- qX`、`qX :- qY` 而无基例）推导结果为空。
主路径用拓扑序单次自底向上（共享节点只触发一次），穷举测试证明两种求值
结果一致。

### 增量维护

- `edit_leaf`：只重放深叶到根的脊线节点（其余共享子树缓存原样保留），
  内容寻址使“未变化”编辑直接复用旧节点。
- `add_rule`：工作队列向父节点传播新状态。
- `delete_rule`：按“最后一条支撑证明消失则撤销状态”的不动点级联，
  状态被撤后再检查其父节点；存在替代证明时状态保留。

### 路径证据

`explain(node, state)` 枚举完整证明树。每条证据带从查询根开始的出现路径
（`$[0][1]...`）。同一个被共享的子树在两个父位置展开为两条路径不同的
证据，不会把不同出现混为一谈。

### 快照

`commit()` 冻结 DAG 克隆 + 状态/证明表 + 接受集合。编辑或删规则后，旧
快照仍可查询状态、接受集与证据；实时视图继续演进。

### 资源上限与取消

`Limits(max_firings, max_rounds, max_evidence, deadline)` 定义预算，超限抛
`LimitExceeded`；`CancelToken` / `CountdownCancel` 协作式取消，抛
`CancellationError`。检查点位于每次规则匹配之前。

## 目录

```
src/statree/conditions.py  属性条件（比较/存在/集合/与或非，声明式解析）
src/statree/model.py       Rule / Signature
src/statree/dag.py         不可变共享 DAG、环检测、hash-consing
src/statree/engine.py      规则索引、触发、最小不动点、证据、上限/取消
src/statree/session.py     Automaton：增量、接受集、快照、深叶编辑
test/test_core.py          核心特性单元测试
test/test_exhaustive.py    独立参考求解器 + 小树穷举对照
examples/demo.py           可重放样例
examples/counterexamples.py 独立可执行反例
```

## 验证策略

`test_exhaustive.py` 内置一个**不复用包内求解器**的独立参考实现（空集
起步同步轮迭代到不动点），在确定性生长的小树宇宙上对每一次规则增删与
叶编辑逐节点比对状态表，并检查深叶编辑只影响脊线、拓扑单次求值与不动点
求值三者一致。
