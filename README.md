# 可保留历史版本的分片文本结构

这是一个零运行时依赖的 TypeScript 实现。文本保存在持久化 AVL Rope 中，叶子节点保存约 128 个 UTF-16 code unit 的分片；编辑只复制经过的树路径，未修改的叶子和子树在多个版本之间按引用共享。

## 核心能力

- `insert`、`delete`、`replace`：返回新版本，旧版本不变。
- 节点缓存 `length`（UTF-16 code unit 数）、`breaks`（LF 数）和 `height`。
- `readRange(start, end)`：惰性生成与范围相交的分片，不扫描或拼接整篇文本。
- `positionAt(offset)` / `offsetAt({ line, column })`：基于 0 起行列和 UTF-16 列互转。
- LF (`\n`) 和 CRLF (`\r\n`) 都按一个换行处理。
- 公共偏移永远是 UTF-16 code-unit 边界，禁止切开 emoji 等代理对。
- 祖孙版本共享未改节点；所有节点 `Object.freeze`，读取和后续编辑不会污染旧版本。

## 文档边界定义

- 公开偏移使用 UTF-16 code unit。例如 `😀` 占 2 个单位，偏移 1 位于高、低代理项之间，会报错。
- 行列均从 0 开始，`column` 也是 UTF-16 code unit 数。
- CRLF 作为一个整体行结束符。偏移 1 指在 CR 前，偏移 2 指在 CR 与 LF 之间，偏移 3 指在 LF 后；偏移 2 对插入、删除、替换和范围读取都会拒绝。
- 本实现选择题目允许的“拒绝”策略：在 CR/LF 中间编辑会抛出包含 `CRLF` 的错误，调用方把边界移动到 CR 前或 LF 后重试。
- 可以整体插入或删除完整的 `\r\n`，但不接受孤立 CR；文档只支持 LF 和 CRLF，避免隐式规范化。
- 行内最大可定位偏移是该行行结束符前：LF 行为 LF 的偏移，CRLF 行为 CR 的偏移。最后一行的最大偏移为文档末尾。

## API 示例

```ts
import { ShardedText } from "./src/index.ts";

const v1 = ShardedText.from("alpha\nbeta\r\n😀");
const v2 = v1.insert(v1.offsetAt({ line: 1, column: 0 }), "still ");
const v3 = v2.delete(6, 12);

for (const chunk of v3.readRange(0, v3.length)) {
  console.log(chunk.start, chunk.end, chunk.text);
}

v3.positionAt(12);       // { line, column }
v3.offsetAt({ line: 2, column: 0 });
v3.lineCount;
v3.lineBreakCount;
v3.length;
```

## 复杂度

| 操作 | 复杂度 | 说明 |
| --- | --- | --- |
| 插入 / 删除 / 替换 | `O(log n + k)` | `k` 为新文本长度，使用分块叶子；旧节点共享 |
| 偏移到行列 | `O(log n + s)` | `s` 为定位到的叶子长度，使用换行缓存 |
| 行列到偏移 | `O(log n + s)` | 按缓存的 LF 数查找第 N 个换行 |
| 范围迭代 | `O(log n + m)` | 只访问与范围相交的叶子，不拼接全文 |
| 整文 `toString()` | `O(n)` | 仅作为调试/校验便利方法 |

## 文件结构

- `src/rope.ts`：不可变叶子/分支、AVL 拼接分裂、边界校验、范围遍历、换行索引。
- `src/index.ts`：公开 `ShardedText` API。
- `test/core.test.ts`：缓存、结构共享、范围迭代、版本隔离。
- `test/boundaries.test.ts`：UTF-16、emoji、LF/CRLF、随机字符串参考模型。
- `examples/demo.ts`：可运行示例。
- `docs/test-results.txt`：内置测试实际输出。
- `docs/example-output.txt`：示例实际输出。

## 运行

需要支持直接运行 TypeScript 的 Node.js（Node 23.6+ 或 Node 22.18+）。本仓库验证环境为 Node v25.5.0。

```bash
npm test
npm run example
```

测试只使用 Node.js 内置的 `node:test` 和 `node:assert/strict`，不需要安装运行时依赖。若本机安装了 TypeScript，也可运行：

```bash
npm run typecheck
```

## 本次验证结果

- `npm test`：13 个测试全部通过，0 失败。
- 300 次确定性随机编辑持续与普通字符串参考模型逐字符一致，并校验 AVL 高度、长度缓存和换行数缓存。
- 大文档单点插入后，除分裂点附近外几乎所有叶子节点保持同一对象引用。
- 完整输出见 `docs/test-results.txt` 和 `docs/example-output.txt`。
