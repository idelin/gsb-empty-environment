# approval-checker

代码审批检查器：按路径规则（覆盖 / 追加所有者）逐文件校验审批阈值，
作者本人的批准不计入，并在最多 20 名候选中求出**最少补充审批人集合**；
无解时指出阻塞文件。重命名文件需同时满足新旧双路径的规则要求。

## 运行

需要 Node.js >= 22.6（直接执行 TypeScript，无需编译）：

```sh
node src/cli.ts examples/input.json   # 或从 stdin 读入
cat input.json | node src/cli.ts
npm test                              # 运行测试
```

退出码：`0` 全部满足，`1` 存在阻塞文件，`2` 输入非法。

## 输入格式

```json
{
  "author": "alice",
  "approvals": ["bob"],
  "candidates": ["carol", "dave"],
  "files": [
    { "path": "src/a.ts" },
    { "path": "new/x.ts", "oldPath": "old/x.ts" }
  ],
  "rules": [
    { "pattern": "src/**", "owners": ["bob", "carol"] },
    { "pattern": "src/core/**", "owners": ["dave"], "mode": "append", "threshold": 2 }
  ]
}
```

- `author`：变更作者；其批准永远不计入阈值，也不会成为候选。
- `approvals`：已给出的批准（可省略）。
- `candidates`：可追加请求的审批人，去重后**最多 20 名**，超出则报错。
- `files[].path` / `files[].oldPath`：变更文件；`oldPath` 表示重命名的原路径，
  新旧路径各自的规则要求都必须满足。
- `rules`：按输入顺序**优先级递增**（后面的规则优先）：
  - `pattern`：glob，支持 `**`（跨目录）、`*`（单段）、`?`（单字符），整路径锚定匹配；
  - `owners`：该规则的所有者；
  - `mode`：`override`（默认）替换累计所有者集合，`append` 并入累计集合；
  - `threshold`：所需批准数，默认 `1`，取最后一个匹配规则的值。

每条路径的有效所有者集合会移除作者本人；未匹配任何规则的路径不产生要求。

## 输出格式

成功时（`ok: true`）：

- `added`：最少补充审批人集合；
- `approvers`：全部生效审批人（已有 + 新增）；
- `files[]`：逐文件报告，含每条路径的 `owners`、`threshold`、实际 `approvals`
  与 `satisfied`。

无解时（`ok: false`，退出码 1）：

- `blocking[]`：阻塞文件列表，含 `path`、失败的 `failedPath`（重命名时可能是
  旧路径）、`owners`、`threshold` 与全部候选都加入后仍可达的 `maxApprovals`。

## 求解方法

将每条路径要求建模为约束（所有者集合 ∩ 审批人 ≥ 阈值），对候选做
迭代加深 + 分支限界搜索（suffix 覆盖剪枝），返回规模最小且按候选输入
顺序确定性的子集。候选上限 20 保证最坏情况也可快速求解。
