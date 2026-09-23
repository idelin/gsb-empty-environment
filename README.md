# treeauto — 带状态约束的有序树自动机（共享子树 DAG）

纯 Python 3.11 标准库实现，不依赖任何现成正则/自动机库。字母表与转移结构
全部显式输入。

## 特性

- **有序树 / DAG**：节点不可变，子树可共享；`validate_dag` 基于身份检测并
  禁止结构环（`StructuralCycleError`）。
- **显式字母表**：标签 → 允许元数；同标签不同元数绝不混用（规则按
  `(label, arity)` 精确匹配）。
- **转移规则**：标签、元数、逐位子状态、属性条件（命名谓词注册表，可序列化）。
- **最小不动点语义**：规则环可收敛求解；无根支撑（无基础规则触发）的循环
  状态不会自我成立。资源上限（步数/迭代/事实数）与协作式取消
  （`CancelToken`）均有定义。
- **状态约束**：`MutexConstraint`、`MaxCountConstraint`，违约阻断接受并记录
  `Violation`。
- **共享缓存**：结构相同的子树只计算一次；`occurrences` 给出每个出现位置，
  `proof(state, path)` 的证据携带完整根路径，区分同一子树的不同出现。
- **增量维护**（`Session`）：
  - `edit` / `replace_at`：沿路径的持久化编辑，深叶修改只重算该路径祖先；
  - `replace_everywhere`：替换所有结构相同子树，重算全部父位置构成的祖先锥；
  - `delete_rules`：撤销失去（良基）支撑的状态并级联；
  - 每次变更推送不可变 `RunResult` 快照，旧快照仍可查询。
- **反例**：`check_expectation` 生成 JSON 可序列化的 `Counterexample`，
  可由 `samples/counterexample_runner.py` 独立重放（退出码 0 = 仍然复现）。

## 运行

```bash
python3 -m unittest discover -s tests -v   # 测试（含 74 棵小树穷举 vs 增量对照）
python3 -m samples.replay_samples          # 确定性可重放样例（输出被测试钉住）
python3 -m samples.counterexample_runner case.json   # 独立执行反例
```

## 结构

- `treeauto/core.py` — 节点/DAG、字母表、谓词注册表、规则、约束、自动机
- `treeauto/evaluation.py` — 不动点引擎、限制/取消、证明与路径证据、快照
- `treeauto/incremental.py` — 增量会话：编辑、DAG 替换、规则删除撤销、快照史
- `treeauto/counterexample.py` — 可序列化反例与重放
- `samples/` — 可重放样例与独立反例执行器
- `tests/` — unittest 套件
