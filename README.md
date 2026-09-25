# 证据驱动的实体合并（Evidence-driven Entity Resolution）

基于**显式等价 / 不等价证据**合并实体；状态完全由一条只增（append-only）的
JSONL 证据日志派生，因此可以随时重放；撤回一条等价边会让实体重新分裂；
`A=B、B=C 且 A≠C` 会检出冲突并返回完整**证据环**；同名记录不会自动合并。

仅依赖 Python 3.11 标准库。

## 运行

```bash
python3.11 run_demo.py                 # 合成数据场景演示（会生成 demo_data/events.jsonl）
python3.11 -m unittest discover -s tests -v
```

## 设计

### 数据与稳定 ID

| 对象 | ID | 版本 |
| --- | --- | --- |
| 事实 Fact（EQ / NEQ） | `sha256:` 内容哈希 = `fact|kind|min(a,b)|max(a,b)|reason` | 每次撤回 +1 |
| 推导步骤 Step | `sha256:` 内容哈希 = `step|rule|a|b|事实ID链` | 规则语义变化时升版 |
| 冲突 Conflict | `sha256:` 内容哈希 = `conflict|kind|环上事实ID` | 规则语义变化时升版 |
| 事件 Event | 日志序号 `seq`（重放后序号一致） | — |
| 规则 Rule | 固定字符串 ID（如 `R-EQ-TRANSITIVE`） | 显式 `version` 字段 |
| 实体 Entity | 代表 ID = 连通分量内**字典序最小节点 ID**（规则 `R-REP-MIN`），与断言顺序无关 | — |

事实与节点身份只由调用方给出的**节点 ID** 决定；`add_name` 登记的只是展示名，
规则 `R-NAME-LABEL` 明确声明同名不合并。

### 事件溯源（可重放）

`EntityRepository(path)` 的每次操作（`ASSERT` / `RETRACT` / `LABEL` / `SEED`）
都追加一行 JSON。派生状态（实体、步骤、冲突）不入库，调用 `rebuild()` 从当前
活跃证据重新计算。`EntityRepository.replay(path)` 仅读取日志即可得到字节级一致
的状态，包括所有稳定 ID 与版本号。

### 推导规则（`entity_kg/rules.py`）

- `R-EQ-EXPLICIT` / `R-NEQ-EXPLICIT`：显式 EQ / NEQ 证据。
- `R-EQ-TRANSITIVE`：同一连通分量内非直接相邻的节点对，生成一条推导步骤，
  `chain` 给出 BFS 树上的最短显式事实链（每一环都是可追溯的显式 Fact ID）。
- `R-NEQ-CYCLE`：一条 EQ 链的两端存在显式 NEQ，构成 `inequality_cycle`，
  返回的 `cycle` 按 `EQ, EQ, …, NEQ` 闭合，且每个 EQ 边附带支撑它的 `step_id`。
- `R-EQ-NEQ-DIRECT`：同一对节点同时存在 EQ 与 NEQ，`direct_contradiction`。
- `R-NEQ-REFLEXIVE`：`a≠a` 违反自反性，`reflexivity`。
- `R-NAME-LABEL`：名字仅作标签，不作为合并依据。
- `R-REP-MIN`：代表 ID 取最小节点 ID。

冲突不会被静默吞掉：默认模式下冲突保留在 `state.conflicts` 中；`strict=True`
时触发冲突的操作抛出 `ConflictError`，载荷即证据环。跨不同分量的 NEQ 是一致
证据，不构成冲突；撤回不等价边后冲突自动消失。

### 撤回导致分裂

`retract(a, b, FactKind.EQ, reason=...)`（或按事实 ID 的 `retract_fact`）只追加
`RETRACT` 事件，事实置为非活跃并升版、记录 `retracted_seq`。重新推导后连通分量
可能断开：例如撤回 `B=C` 后，`{A,B,C}` 分裂为 `{A,B}` 与 `{C}`，相关传递步骤与
冲突环一并消失。撤回后可用新理由重新断言，形成独立的事实版本链。

## 代码结构

```
entity_kg/
  model.py        # Fact / Event / Step / Conflict / Entity 及内容哈希 ID
  rules.py        # 带版本的命名规则目录
  derive.py       # 纯函数：活跃证据 -> 实体 / 步骤 / 冲突环（确定性）
  repository.py   # 事件日志、撤回、版本、JSONL 持久化与 replay
run_demo.py       # 合成数据运行样例
tests/test_entity_kg.py  # 23 个 unittest 用例
```
