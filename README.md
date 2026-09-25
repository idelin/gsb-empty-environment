# json-path-move

按模式匹配路径对 JSON 兼容树执行重命名/移动：先基于原始树快照生成完整计划，
再做目标冲突检查，最后一次性应用。规则输出不会被再次匹配，因此不会递归重写
新生成的节点（如 `a -> a/b` 安全终止）。

纯 TypeScript（可擦除语法），零依赖；Node.js >= 23.6 可直接运行 `.ts`。

## 运行

```sh
npm test    # node --test（内置测试运行器）
npm run demo
```

## 路径语法

- 段之间用 `/` 分隔；空字符串表示根。
- 键中字面量的 `/` 与 `\` 分别转义为 `\/`、`\\`（如 `a\/b/*` 匹配键 `a/b` 的子级）。
- 数组元素用十进制下标寻址（`list/0`），对象键无序、数组有序。
- 限制：根级的空字符串键无法与根路径区分（嵌套位置的空键可用）。

## 规则与通配符

规则为 `{ from, to }` 模式对：

- `*` 匹配恰好一段并捕获；`**` 匹配零段或多段并捕获（贪心 + 回溯）。
- `to` 中的 `*` / `**` 按出现顺序消费 `from` 的同类捕获，数量必须一致，
  否则抛出 `RuleError`（消息含规则序号与两侧模式）。
- 每个节点取第一条匹配的规则；节点一旦匹配，其子树整体移动，不再深入匹配。
- 匹配只发生在原始树快照上，计划生成后不链式触发。

## 冲突检测（应用前）

以下情况抛出 `MoveConflictError`（携带 `targetPath` 与 `sourcePaths`，均可定位到具体路径）：

- 两个源把**不同**的值移到同一目标（值相同则静默合并，幂等）；
- 一个目标是另一个目标的严格前缀（目标互相嵌套）；
- 目标位置在移除源之后仍存在且值不同（如 `a/* -> b/*` 时 `b/x` 已有不同的值）。

移动是同时语义：所有源先从快照移除，再写入所有目标，因此
`a->b, b->c` 得到 `{b: 旧a, c: 旧b}`，不会链式传递。
被移空的容器会被清理（原本就为空的容器保留）；数组按序压缩。

## API

```ts
import { movePaths, planMoves, applyMoves } from './src/index.ts';

const { tree, plan } = movePaths(input, [{ from: 'a/*', to: 'b/*' }]);
// plan: [{ fromPath, toPath, value, ruleIndex, ... }] —— 可审计后再 applyMoves
```

输入树不会被修改；错误类型：`PathSyntaxError`（含偏移）、`RuleError`（含规则序号）、
`MoveConflictError`（含目标/源路径）。

## 样例输出（`npm run demo`）

```
1) a/* -> b/*  plan: [ "a/x => b/x", "a/y => b/y" ]
1) result: { "keep": "untouched", "b": { "x": 1, "y": [true, null] } }
2) escaped key plan: [ "a\/b/c\/d => out/c\/d" ]
3) a -> a/b result: { "a": { "b": { "x": 1 } } }        // 祖先->子孙，安全终止
4) conflict detected: targetPath "b/x", sourcePaths ["a/x"]  // 目标已有不同值
5) glob + array: { "list": [2, 3], "dist": {...}, "first": 1 }
```

## 测试结果

`node --test`：32 个测试全部通过（路径转义、模式/捕获、冲突定位、
祖孙移动终止、数组保序、同时移动不链式、输入不可变等）。
