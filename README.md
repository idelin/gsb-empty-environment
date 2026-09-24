# pairwise-ot

仅含**插入 / 删除**两类操作的双端操作变换（OT），TypeScript 实现，零依赖，
直接由 Node.js（>= 22.6，类型擦除）运行，测试使用 Node 内置 `node:test`。

## 模型

- 文档为 JS 字符串；**所有公开偏移均为 UTF-16 码元下标**（即普通字符串下标）。
- 操作携带**基线版本** `baseVersion` 与**站点序号** `siteId`（见 `VersionedOp`）。
- `Session` 维护线性版本历史：收到基于旧版本的操作时，将其沿历史**向前变换**后应用；
  基线为负、非整数或超过当前版本时抛出 `RangeError`（无效基线）。

## 变换规则（`src/ot.ts`）

- **插入 vs 插入**：同位置时站点序号小者的文本排在前面，与交付顺序无关（稳定次序）。
- **插入 vs 删除**：插入点落在被删区间内时，钉到区间起点。
- **删除 vs 插入**：插入落在删除区间内部时，删除**分裂为两段**。
- **删除 vs 删除**：重叠部分只删一次；未被覆盖的片段保留（可能剩 0、1、2 段）。
  分裂出的片段按**从右到左**发出，保证顺序应用时坐标始终合法。
- **代理对保护**：任何会切开 UTF-16 代理对的偏移（插入点或删除区间端点）都会被拒绝；
  插入文本中的孤立代理同样被拒绝。LF (`\n`) 与 CRLF (`\r\n`) 均按普通字符处理。

## 使用

```ts
import { insert, del, transform, applyOp, applyOps } from './src/ot.ts';
import { Session } from './src/session.ts';

// 纯变换（TP1：两种交付顺序结果一致）
const left  = applyOps(applyOp(doc, opA), transform(opB, siteB, opA, siteA));
const right = applyOps(applyOp(doc, opB), transform(opA, siteA, opB, siteB));

// 带版本的会话
const session = new Session('hello\r\nworld');
session.receive({ op: insert(0, '>> '), baseVersion: 0, siteId: 1 });
session.receive({ op: del(2, 2), baseVersion: 0, siteId: 2 }); // 旧基线，自动前滚
```

## 运行

```sh
npm test    # node:test 测试（含 500 组随机 TP1 收敛用例）
npm run demo
```
