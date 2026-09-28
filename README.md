# 离线同步重命名仲裁（Offline Rename Arbiter）

纯 Python 3.11 标准库实现的离线目录树同步仲裁组件。目录树中的每个对象
（文件或目录）拥有**稳定对象 ID**；同步事件包含四种操作、**因果依赖**
（`deps`）和**全序决胜戳**（`stamp`）。组件对任意符合因果关系的事件投递
顺序产出**完全一致**的收敛结果。

## 运行方式

```bash
# 方式一：可执行脚本（读文件）
./rename-arbiter examples/events.json

# 方式二：标准输入 / 标准输出（以模块方式运行）
cat examples/events.json | PYTHONPATH=src python3.11 -m rename_arbiter

# 可选参数：-o 输出文件；--compact 单行紧凑 JSON
./rename-arbiter examples/events.json -o report.json --compact
```

退出码：`0` 成功；`2` 输入 JSON 非法、事件格式错误或因果图存在环。

运行测试（无需第三方依赖）：

```bash
python3.11 -m unittest discover -s tests -v
```

## 输入格式

输入为一个 JSON 数组，每个元素是一个事件对象：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 事件唯一 ID |
| `type` | string | `create` / `move` / `rename` / `delete` |
| `object_id` | string | 稳定对象 ID（`root`、`__conflicts__` 为系统保留） |
| `stamp` | object | 决胜戳 `{"value": 整数, "node": 字符串}`，按 `(value, node)` 字典序全序比较 |
| `deps` | string[] | 因果依赖的事件 ID 列表（须发生在本事件之前） |
| `is_dir` | bool | 仅 `create` 使用，是否为目录（默认 false） |
| `new_parent_id` | string | `create`/`move` 必填，新父目录 ID，根目录为 `"root"` |
| `new_name` | string | `create`/`rename` 必填，新名字（不含 `/`、NUL、`.`、`..`） |

- `create`：要求 `new_parent_id` + `new_name`。
- `move`：要求 `new_parent_id`（移动到新目录，名字不变）。
- `rename`：要求 `new_name`（在当前目录内改名）。
- `delete`：仅需要 `object_id`；删除目录会递归删除整个子树。

`deps` 只允许引用输入批次中存在的其它事件 ID（不可自依赖、不可成环）。

## 输出格式

输出一个 JSON 对象：

- `order`：实际重放所用的**规范因果全序**（Kahn 拓扑 + 就绪堆按戳排序）。
- `tree`：收敛后的嵌套目录树（墓碑不出现）；若存在冲突，根下第一个节点是
  系统管理的 `.conflicts` 冲突目录（`object_id` 为 `__conflicts__`）。
- `placements`：扁平视图，逐项给出 `object_id / is_dir / parent_id / name /
  placed_by / in_conflict`。
- `tombstones`：所有已删除对象及删除它的事件 `deleted_by`。
- `conflicts`：同名仲裁记录（见下“冲突原因”）。
- `rejected`：被拒绝事件，含 `reason` 机器码与人类可读 `detail`。
- `summary`：事件总数、已应用数、被拒绝数、冲突数。

## 仲裁语义（设计决策）

1. **收敛性**：投递顺序无关。内部先求因果 DAG 的规范线性扩展（就绪事件按
   `(stamp, event_id)` 弹出），因此无论按何种因果合法顺序投递，重放顺序、
   最终树、冲突与拒绝报告都逐字节一致。测试对验收场景做了全部 `4! = 24`
   种排列，并对更大场景做了 200 次随机洗牌比对。
2. **并发同目录同名冲突**：两个无因果关系的放置事件争用同一
   `(父目录, 名字)` 槽位时，**戳大的对象保留原名**；败者（若为目录则连同
   整个子树）被移动到根下 `.conflicts/`，名字形如
   `原名__对象ID__致败事件ID-序号`，并输出一条带原因、双方戳和落点路径的
   冲突记录。戳的 `value` 相同则用 `node` 字典序决胜，保证全序无平局。
3. **因果优先于戳**：若新事件在因果上发生在当前占位事件之后（占位事件在其
   依赖闭包中），则无论戳大小，新事件生效，原占位者进入冲突目录，原因记为
   `CAUSAL_NAME_SUPERSESSION`。
4. **禁止目录成环**：把目录移动到其自身或任意后代目录下会被拒绝（原因
   `CYCLE`），树结构保持不变。该检查在任何状态修改之前完成。
5. **删除墓碑不被旧事件复活**：
   - 作用在已删除对象上的过期 `move`/`rename`/重复 `delete` 一律拒绝
     （`ALREADY_DELETED` / `OBJECT_NOT_FOUND`）。
   - 同一对象 ID 只有在**显式 `create` 且该 create 的依赖已包含墓碑事件**
     时才允许复活（新的一次“现身”）；与删除并发或更早的 create 被拒绝
     （`CONCURRENT_RECREATE_REJECTED`）。这是“删除在并发下获胜”的保守策略。
6. 删除目录递归墓碑化其全部现存后代。

### 冲突原因（`conflicts[].reason`）

- `CONCURRENT_NAME_COLLISION`：并发同名，高戳获胜，败者进入 `.conflicts`。
- `CAUSAL_NAME_SUPERSESSION`：因果上更晚的放置取代已有占位。

### 拒绝原因（`rejected[].reason`）

`CYCLE`、`TARGET_NOT_DIRECTORY`、`TARGET_NOT_FOUND`、`TARGET_DELETED`、
`OBJECT_NOT_FOUND`、`OBJECT_ALREADY_EXISTS`、`CONCURRENT_RECREATE_REJECTED`、
`ALREADY_DELETED`。

## 验收边界（对应测试）

- 对象 `a`、`b` 并发改为同名：戳 `(10,n1)` 对 `(11,n2)`，所有 24 种投递
  排列下都是 `b` 以 `same.md` 胜出、`a` 进入 `.conflicts`
  （`tests/test_arbiter.py::ConcurrentRenameTests`）。
- 目录 `x` 移到其后代 `y`（及更深的 `y/z`）下：以 `CYCLE` 拒绝且不成环，
  且任意投递顺序结果一致（`tests/test_arbiter.py::CycleTests`）。
- 删除后过期改名/移动被拒；并发复活被拒、依赖墓碑的显式重建被允许
  （`tests/test_arbiter.py::TombstoneTests`）。

## 目录结构

```
rename-arbiter                 # 可执行命令行入口
examples/events.json           # 合成示例数据（含冲突、成环、墓碑场景）
src/rename_arbiter/model.py    # 事件模型与输入校验
src/rename_arbiter/engine.py   # 因果拓扑 + 仲裁引擎
src/rename_arbiter/cli.py      # 命令行
tests/test_arbiter.py          # 验收边界与核心语义测试
tests/test_semantics.py        # 其余语义、收敛性、CLI 测试
```

## 说明与边界

- “最优”未作形式化证明：该策略是**一种**收敛的仲裁策略（LWW 决胜 + 败者入
  冲突目录 + 删除并发获胜），其正确性（收敛性、无环、不复活）通过排列/洗牌
  测试验证，而非数学证明；决胜策略本身也不是唯一可选策略。
- 批次内须包含事件所引用的全部依赖事件；跨批次的完整状态快照持久化不在本
  组件范围内。
- 所有示例数据均为人工合成数据。
