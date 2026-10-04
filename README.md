# 事件重排器（event-reorderer）

在两类约束下求**字典序最小**的事件全序（最多 22 个事件）：

1. **部分先后约束**：显式给出的 `before -> after` 边；
2. **对象生命周期状态约束**：每个对象遵循 `create -> update* -> delete`
   状态机——`update`/`delete` 要求对象存活，`create` 要求对象尚未创建，
   对象被删除后不允许再有任何事件。没有 `create` 事件的对象视为初始即存活。

## 运行

无需安装依赖（Node >= 23，直接擦除类型运行 TypeScript）：

```sh
node src/cli.ts examples/solvable.json     # 或 npm run solve -- examples/solvable.json
node src/cli.ts < input.json               # 从标准输入读取
npm test                                   # 运行测试
```

退出码：`0` 有解，`1` 无解，`2` 输入非法。

## 输入格式

```json
{
  "events": [
    { "id": "A", "object": "o1", "op": "create" },
    { "id": "B", "object": "o1", "op": "delete" }
  ],
  "precedence": [["A", "B"]]
}
```

- `events`：1–22 个，`id` 唯一；同一对象至多一个 `create`、一个 `delete`；
- `precedence`：`[before, after]` 对，可选；
- 字典序按事件 `id` 的字符串（UTF-16 码元）顺序比较整个序列。

## 输出

有解：

```json
{ "status": "ok", "order": ["A", "D", "C", "B"] }
```

无解——输出**阻塞前缀**及每个剩余事件被阻塞的具体约束，而不是笼统的整体无解：

```json
{
  "status": "unsat",
  "blockingPrefix": ["A", "B", "D"],
  "blocked": [
    { "event": "C", "reasons": ["state: object 'o2' is already deleted (by 'D')"] }
  ],
  "cycle": null
}
```

- `blockingPrefix`：字典序最小的**极大局部合法前缀**——前缀内每一步都满足
  先后与状态约束，但再往下任何事件都被阻塞；
- `blocked`：每个未放置事件的具体阻塞原因（等哪个前驱 / 对象未创建 / 已删除）；
- `cycle`：若先后约束成环，给出其中一个环。

## 算法

状态约束不是事后检查（不是“先拓扑排序再发现状态不合法就报整体无解”），
而是与先后约束一起融入逐步构造：

- **有解判定与构造**：字典序贪心 + 可行性预言机。每个位置按字典序尝试
  局部合法（前驱已放置、状态迁移合法）的事件，并用一个 DFS + 位掩码记忆化
  的预言机检查剩余事件是否仍存在合法完成；第一个可完成者即被选中。
  归纳可得结果为字典序最小的合法全序。预言机能避开“局部合法但通向死路”
  的选择（例如过早的 `delete`）。
- **无解诊断**：改用纯局部合法贪心，走出字典序最小的极大局部合法前缀作为
  阻塞前缀，并为每个剩余事件列出具体阻塞约束。

复杂度：预言机按已放置集合的位掩码记忆化，状态数上界 `2^22`，
22 个事件规模下实际运行为毫秒级。
