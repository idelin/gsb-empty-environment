# proofdag — 等价证明 DAG 的规范化、共享与重放

纯 Python 3.11 标准库实现，测试使用 `unittest`。样例数据为合成数据。

## 设计要点

- **规范化（`proofdag/canonical.py`）**：对证明 DAG 去重、按结构键排序并确定性编号；
  摘要是规范化序列化的 SHA-256，与遍历/插入顺序无关；循环图抛出 `CycleError`。
- **交换律与有序共存**：`Rule(commutative=True)` 的前提按无序集合规范化（交换合取前提
  不改变摘要）；`commutative=False` 的前提顺序敏感，顺序不同的证明绝不合并。
- **来源身份独立**：`Fact` 的结构键包含 `source_id`，同值不同来源不合并；
  替换来源必然改变规范结果；内部节点（步骤名）重命名不影响结果。
- **结构共享（`proofdag/store.py`）**：节点按结构键驻留，多引用子证明只存一次，
  反向引用（父集合）完整；`remove_edge` 增量删边后，不再可达的共享子图被级联撤销。
- **摘要碰撞按结构验证**：`DigestRegistry` 以摘要为桶索引，注册与解析时始终做完整
  结构比较，强制碰撞下两个不同证明保持独立条目。
- **稳定 ID 与版本**：`Fact` / `Rule` / `Step` 均带 `*_id` 与 `version`；
  规范编号在规范形式内提供确定性 ID。
- **证据可重放（`proofdag/replay.py`）**：证据为 JSON 可序列化字典，重放时重建图、
  重新规范化并通过注册的规则语义重推每条结论；篡改或循环证据被拒绝。

## 目录结构

```
proofdag/
  model.py       # Fact / Rule / Step（稳定 ID + 版本）
  canonical.py   # 结构键、规范编号、摘要、DigestRegistry、循环检测
  store.py       # 结构共享、反向引用、增量删边与级联回收
  replay.py      # 证据重放与规则语义校验
  demo.py        # 合成数据样例（python -m proofdag）
tests/           # unittest 测试（28 个用例）
```

## 运行

```console
$ python3.11 -m proofdag
== synthetic proof DAG ==
interned nodes: 5
swapped conjunctive premises intern to same node: True
nodes after re-adding swapped step: 5
canonical nodes: 5, roots: (4,)
canonical digest: 1adb14a42a7a4a7af8b423ede31c1e99234fd1b70aa9ddd32d63092a794aaf2f
evidence replay ok: True
nodes with shared subproof referenced twice: 6
back-references of shared conj node: 2
after removing one edge, conj still reachable: True
after removing its root, chain-2 revoked: True
shared conj node still alive via other root: True
final node count: 5
```

## 测试

```console
$ python3.11 -m unittest discover -s tests -v
...
----------------------------------------------------------------------
Ran 28 tests in 0.003s

OK
```

覆盖：交换前提置换摘要不变、有序前提不合并、交换/有序混合共存、内部节点重命名与
输入排列不变性、来源替换改变结果、同值不同来源不合并、循环与悬空引用拒绝、
强制摘要碰撞按结构验证、共享子证明单次存储与完整反向引用、增量删边级联撤销、
证据 JSON 往返重放、篡改/语义违例/循环证据拒绝。
