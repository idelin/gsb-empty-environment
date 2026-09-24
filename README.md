# persistent-text-rope

可保留历史版本的分片文本结构（persistent rope），TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。要求 Node >= 22.6
（依赖原生 TypeScript 类型擦除直接运行 `.ts`）。

## 文档化语义

- **公开偏移一律为 UTF-16 code unit**（与 `String.length`、LSP `character` 一致）。
- **节点缓存**：每个节点缓存 `length`（UTF-16 长度）与 `newlines`（子树中 `\n` 的数量）。
  行统计以 `\n` 为唯一换行符：CRLF 计为一次换行，孤立的 `\r` 不计。
- **编辑边界规则（选择“拒绝”而非拆分重索引）**：
  - 插入偏移、删除/替换的两个端点，**不得落在 CRLF 的 `\r` 与 `\n` 之间**；
  - 也**不得切开 UTF-16 代理对**（high/low surrogate 之间）。
  - 违反时抛出 `RangeError`；在 `\r` 之前或 `\n` 之后编辑是合法的。
  - 读取与行列转换是全函数：`[0, length]` 内任意偏移都可 `positionAt`/`offsetAt` 互转。
- **持久化与结构共享**：`insert`/`delete`/`replace` 返回新 `TextDocument`，
  未修改的子树按引用共享（AVL 平衡的 split/concat，单次编辑 O(log n) 个新节点），
  旧版本永不被改写，任意版本读取互不污染。
- **范围迭代不拼接全文**：`chunks(start, end)` 返回生成器，按叶块产出切片，
  跳过的子树不下钻；`getText()` 只是便捷的全量 join。
- **行列定位**：`positionAt(offset)` 借助缓存的换行数 O(log n) 下钻；
  `offsetAt({line, character})` 反向转换，`character` 为行内 UTF-16 列，
  越界抛 `RangeError`。emoji（代理对）前后偏移往返可逆。

## API

```ts
const v1 = TextDocument.from("one\ntwo\r\nthree 😀");
const v2 = v1.insert(4, "INSERTED ");      // 新版本，v1 不变
const v3 = v2.replace(0, 3, "1");          // 祖孙三版本共存
v2.delete(0, 4);                           // 半开区间 [start, end)
v1.positionAt(15);                         // -> { line, character }
v1.offsetAt({ line: 2, character: 6 });    // -> UTF-16 offset
for (const chunk of v3.chunks(100, 500)) { /* 分块读取，不拼全文 */ }
```

## 运行

```sh
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## 结构

- `src/rope.ts` — 不可变 rope 节点、split/concat 平衡、编辑/定位/迭代、`TextDocument`
- `test/rope.test.ts` — 16 个测试：边界规则、行列互转、范围迭代、三版本共享、
  以及与纯字符串参照实现的随机差分测试（LF/CRLF/emoji 混合）
- `examples/demo.ts` — 可运行样例
