# event-reorderer

事件重排器：在**部分先后约束**（precedence）与**对象创建/更新/删除状态约束**同时成立的前提下，
求事件 id 序列**字典序最小**的全序；事件数最多 22 个。无解时输出**阻塞前缀**及造成阻塞的约束。

## 问题语义

每个事件形如 `{ "id": "e1", "object": "order", "op": "create" | "update" | "delete" }`。
每个对象隐含一个状态机：

- 若某对象存在 `create` 事件，则它初始为 `absent`（不存在）；否则视为预先存在（`present`）。
- `create`：仅当 `absent` 时合法，之后转为 `present`。
- `update`：仅当 `present` 时合法，状态不变。
- `delete`：仅当 `present` 时合法，之后转为 `deleted`，该对象不允许再有任何事件。

由此自然推出：同一对象上 `create` 必须先于其 `update`/`delete`，`delete` 必须是该对象最后一个事件，
重复 `create`、重复 `delete` 均非法。

先后约束 `precedence: [[a, b], ...]` 表示事件 `a` 必须排在事件 `b` 之前。

字典序：按事件 id 的字符串序比较整个序列（如 `["b","a"] < ["c","a"]`）。

## 算法（为什么不是"先拓扑排序再判状态"）

两类约束在搜索的每一步**联合**检查，而不是先做一次拓扑排序、再把状态不合法当作整体无解：

1. **精确可行性预言机**：`feasible(remainingMask)` 用位掩码记忆化 DFS 判断剩余事件集合
   是否存在同时满足先后约束与状态机的合法完成。对象状态是"已发射事件集合"的纯函数
   （沿任意合法路径都相同），因此按剩余集合做 memo 是精确的。`n ≤ 22`，状态数上界 `2^22`。
2. **字典序贪心构造**：每个位置按 id 升序尝试候选事件，仅当"先后约束满足 ∧ 状态迁移合法
   ∧ 剩余集合仍可行"时才发射。不变式 `feasible(remaining)` 保证每步都有可选事件，
   经典交换论证保证得到的全序字典序最小。
3. **无解诊断**：若根节点不可行，则以"局部合法（先后满足 ∧ 状态迁移合法）"贪心发射
   最长前缀作为阻塞前缀，随后报告：
   - `blocked`：每个剩余事件未被满足的直接前驱、以及状态机层面的违规描述；
   - `precedenceCycle`：剩余事件中处于（或依赖于）先后约束环的事件（Kahn 剥离）；
   - `constraints.precedence` / `constraints.state`：阻塞点处仍未满足的先后约束对、
     以及前驱已全部满足却被状态机挡住的事件及其原因。

复杂度：最坏 `O(2^n · n)` 时间、`O(2^n)` 记忆化空间（`n ≤ 22`）；典型实例远小于上界。

## 输入 / 输出

输入（JSON，文件或 stdin）：

```json
{
  "events": [{ "id": "e1", "object": "order", "op": "create" }],
  "precedence": [["e1", "e2"]]
}
```

可解时输出 `{ "ok": true, "order": [...] }`（退出码 0）；
无解时输出 `{ "ok": false, "blockingPrefix": [...], "remainingEvents": [...], "blocked": [...],
"precedenceCycle": [...], "constraints": { "precedence": [...], "state": [...] } }`（退出码 1）；
输入非法（超过 22 个事件、id 重复、未知 op、先后约束引用未知事件等）抛出 `InvalidInputError`，
CLI 退出码 2。

## 运行

需要 Node.js ≥ 22.6（直接以 `--experimental-strip-types` 运行 TypeScript），
或用 `npm i -D typescript @types/node` 后 `npm run typecheck` / 自行编译。

```sh
# 运行测试（单元 + 与暴力枚举对照的随机模糊测试 + CLI 冒烟）
npm test

# CLI
node --experimental-strip-types src/cli.ts examples/solvable.json
cat examples/blocked.json | node --experimental-strip-types src/cli.ts
```

作为库使用：

```ts
import { solve } from "./src/index.ts";

const result = solve({ events: [...], precedence: [...] });
```

## 文件结构

- `src/types.ts` — 输入/输出类型与 `InvalidInputError`
- `src/solver.ts` — 求解器（联合约束贪心 + 位掩码可行性预言机 + 阻塞诊断）
- `src/cli.ts` — 命令行入口（JSON in / JSON out）
- `test/solver.test.ts` — 单元测试、暴力枚举对照模糊测试、CLI 冒烟测试
- `examples/` — 可解与无解示例
