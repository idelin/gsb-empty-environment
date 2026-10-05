# 状态历史对账工具

从多来源事件日志重建业务对象的最终状态。日志允许**缺项**、**重复**和**不准的时间戳**，
但每个事件保留**因果引用**（`causes`）。工具枚举所有合法重建（因果偏序的线性扩展），
收集其最终状态集合；**集合唯一时才给出确定结论**，否则列出全部可能终态。
发现矛盾时返回**最小矛盾事件子集**。时间戳绝不参与排序。

## 输入

JSON（文件路径作为参数，或从 stdin 读取）：

```json
{
  "events": [
    {"id": "e1", "time": 100, "source": "db", "causes": [],
     "op": {"kind": "set", "field": "status", "value": "open"}},
    {"id": "e2", "time": 50, "causes": ["e1"],
     "op": {"kind": "add", "field": "version", "delta": 1}}
  ]
}
```

- `op.kind`：`set`（赋值 `value`）、`add`（数值加 `delta`，缺省从 0 起）、`del`（删字段）
- `causes`：必须先于本事件应用的事件 id；指向缺失事件的引用被容忍并在 `missing` 中报告
- `time` / `source`：传输元数据，不影响任何顺序判断

## 输出

| `status` | 含义 | 退出码 |
|---|---|---|
| `unique` | 所有合法重建收敛到同一终态，见 `state` | 0 |
| `ambiguous` | 存在多个合法终态，全部列于 `states` | 2 |
| `contradiction` | 约束不可满足，`subset` 为最小矛盾事件集 | 1 |

矛盾分两类：同一事件 id 出现内容冲突的记录（最小子集为该 id），
以及因果环（最小子集为最短环上的事件；自依赖的子集大小为 1）。

## 方法

1. **去重**：按 id 归并记录；语义内容（`causes` + `op`）一致视为重复，冲突即矛盾。
2. **建图**：仅对在场事件建立因果边；缺失引用记为 `missing` 后忽略。
3. **环检测**：Floyd-Warshall 求全源最短路，返回最短有向环作为最小矛盾子集。
4. **子集 DP**：`reachable[mask]` = 以合法顺序应用 `mask` 中事件可达的状态集合
   （状态经规范化序列化去重并内化，转移 `(state, event)` 缓存），
   `reachable[全集]` 即所有合法重建的终态集合。

事件数上限 20（`MaxEvents`），最坏情况（20 个无约束事件）约 2 秒。

## 使用

```sh
go test ./...          # 运行测试
go build -o reconcile . && ./reconcile input.json
cat input.json | ./reconcile
```
