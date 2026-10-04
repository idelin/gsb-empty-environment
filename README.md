# 网络故障解释器 (network-fault-explainer)

根据网络拓扑和路径探测结果，推断满足全部观测的最少故障边集合。

- 成功观测（`ok`）：路径上所有边均正常。
- 失败观测（`fail`）：路径上至少有一条边故障。
- 输出全部并列的最少故障边解；若观测互相矛盾，输出最小不可满足观测子集。
- 拓扑最多支持 25 条边。

## 运行

需要 Node.js >= 23.6（原生运行 TypeScript，无需编译）。

```sh
node src/cli.ts examples/network.json   # 从文件读取
cat examples/network.json | node src/cli.ts   # 从标准输入读取
npm test                                # 运行测试
```

## 输入格式

```json
{
  "topology": {
    "links": [{"id": "e1", "from": "A", "to": "B"}, "e2"]
  },
  "observations": [
    {"id": "probe1", "path": ["e1", "e2"], "status": "fail"},
    {"id": "probe2", "path": ["e2"], "status": "ok"}
  ]
}
```

- `links`：边列表，元素为边 id 字符串或含 `id` 的对象（`from`/`to` 可选，仅作记录）。
- `observations[].path`：该次探测经过的边 id 序列。
- `observations[].status`：`ok` 或 `fail`。

## 输出格式

可满足时：

```json
{
  "status": "ok",
  "minFaults": 1,
  "solutions": [["e4"], ["e2"]]
}
```

矛盾时（某条失败路径的所有边都被成功观测覆盖）：

```json
{
  "status": "contradiction",
  "minUnsatObservationSubsets": [["probe-ad", "probe-ab", "probe-cd"]]
}
```

`minUnsatObservationSubsets` 列出全部观测数最少、且自身即不可满足的观测子集。

输入非法（JSON 错误、边数超过 25、路径引用未知边等）时，向 stderr 输出
`{"status": "error", "message": ...}` 并以退出码 1 结束。

## 算法

- 最少故障边 = 失败路径（扣除被成功观测强制为正常的边）上的最小命中集，
  用位掩码分支限界枚举全部最小解；稠密实例自动切换为对偶的
  "最大不含失败路径的边集" 搜索。
- 最小不可满足观测子集 = 1 条失败观测 + 覆盖其路径所需的最少成功观测
  （最小集合覆盖），对所有失败观测取最小并枚举全部并列核。
