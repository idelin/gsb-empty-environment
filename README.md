# 有限字母路径合规（dfa_path）

在有向多重图中寻找从 `s` 到 `t` 的**路径标签被给定 DFA 接受**的最低成本走法（允许重复顶点）。
仅依赖 Python 3.11 标准库。所有示例数据均为合成数据。

## 运行

```sh
python3 dfa_path.py [input.json] [--pretty]   # 省略文件或传 "-" 时从 stdin 读取
python3 -m unittest -v test_dfa_path          # 运行测试
```

退出码：`0` 正常（含未找到路径）；`2` 输入/规格错误。

## 输入格式（JSON）

```json
{
  "alphabet": ["a", "b"],
  "graph": {
    "vertices": ["s", "m", "t"],
    "edges": [
      {"id": 0, "src": "s", "dst": "t", "label": "a", "cost": 1}
    ]
  },
  "dfa": {
    "states": ["q0", "q1"],
    "start": "q0",
    "accept": ["q1"],
    "transitions": {"q0": {"a": "q1"}}
  },
  "source": "s",
  "target": "t"
}
```

- `alphabet`：非空、互不相同的单字符列表。
- `graph.vertices`：互不相同的字符串；`graph.edges` 允许平行边与自环。
- 边：`id` 唯一且类型一致（全 int 或全 str）；`src`/`dst` 必须是已声明顶点；
  `label` 必须是字母表中的单字符；`cost` 为非负数值（int/float，拒绝 bool）。
- `dfa.transitions` 为部分函数 `{状态: {字符: 状态}}`；缺失的转移视为拒绝（隐式死状态）。
- `source`/`target` 必须已声明；二者可相同（起点接受时空走法合法，长度 0）。

## 输出格式（JSON）

找到时：

```json
{
  "found": true,
  "cost": 2,
  "edge_count": 2,
  "labels": "ab",
  "edge_ids": [1, 2],
  "vertex_path": ["s", "m", "t"],
  "dfa_state_path": ["q0", "q1", "q2"]
}
```

- `vertex_path`（图路径证据）与 `dfa_state_path`（自动机状态路径证据）为双证据：
  对每个 i，`edge_ids[i]` 连接 `vertex_path[i] -> vertex_path[i+1]`，其标签
  `labels[i]` 使 DFA 从 `dfa_state_path[i]` 转移到 `dfa_state_path[i+1]`，
  且末状态属于 `accept`。
- 未找到时输出 `{"found": false}`。

## 优先级与最优性

比较键为 `(总成本, 边数, 标签序列, 边ID序列)` 的字典序，即依次：
最低成本 → 最少边 → 标签序列字典序 → 边 ID 序列字典序。

算法在乘积图 `(顶点, DFA状态)` 上运行 Dijkstra，堆键即上述元组。
由于边成本非负，任意路径扩展一条边后键严格增大（成本增大，或成本不变时边数增大），
因此键关于路径前缀单调，Dijkstra 的"弹出即最优"论证成立：
每个乘积状态至多结算一次，算法必然终止——**零成本环不会导致无限搜索**
（沿零成本边扩展会使边数 +1，键严格增大，已结算状态不会被重新改进）。
乘积状态数有限（`|V| × |Q|`），故第一次弹出满足 `顶点 == t 且 状态 ∈ accept`
的状态即为全局最优键，对应路径即答案。

## 验收边界示例

`examples/accept_boundary.json`：图中有 `s -> t` 标 `a` 成本 1 的直达边，
但 DFA 只接受 `ab`，因此不能直接返回；合法答案为经中间节点 `m` 的
`a,b`，总成本 2（见 `AcceptanceBoundaryTest`）。

## 文件

- `dfa_path.py`：求解器与 CLI。
- `test_dfa_path.py`：unittest 套件（验收边界、零成本环、三级平局、结构边界、输入校验、CLI）。
- `examples/`：合成示例输入。
