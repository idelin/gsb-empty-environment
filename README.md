# Peritext/Fugue 风格富文本格式标注 CRDT（TypeScript）

富文本粗体、斜体、下划线、超链接等**可重叠格式标注**的 CRDT 合并器。
解决多人并发在同一字符区间添加/取消冲突样式时的边界因果判定问题，保证所有副本强收敛。

## 设计

两层结构（对应 Peritext + Fugue/RGA 的经典组合）：

1. **字符序列层（RGA）**：每个字符拥有全局唯一且全序的 ID `<lamport>@<actor>`；
   删除是墓碑（tombstone），并发插入按 ID 确定性排序，与到达顺序无关。
2. **格式标注层（Peritext）**：每条格式操作（mark）是由一对**锚点**界定的区间
   `{attr, value, startAnchor, endAnchor}`；锚点引用字符 ID 而非数组下标，
   因此并发插入/删除不会让区间漂移。`value = null` 表示“取消该格式”（如取消加粗）。

### 边界因果判定（冲突解决）

- 同一字符、同一属性被多条 mark 覆盖时，按 **(lamport, actor)** 取最大者生效（LWW）：
  - 因果上**后发生**的操作（lamport 更大）必然获胜；
  - **并发**操作（lamport 相同）按 actor 字典序确定性裁决，所有副本结果一致。
- 不同属性（bold vs underline）互不冲突，直接叠加——因此并发 "A 加粗 AB" 与
  "B 下划线 BC" 合并后，重叠字符 B 同时携带两种格式，无格式断裂。
- 锚点分 `before`/`after` 两侧，实现 Peritext 的边界扩展语义：
  在格式区间**末尾**边界键入会继承格式（bold/italic 等默认），
  在**起始**边界键入不继承；超链接默认两侧都不扩展。可通过 `expand` 参数覆盖。

## 文件结构

- `src/richtext.ts` — CRDT 实现（`RichText` 类：`insert/delete/format/getSpans/apply/merge`）
- `test/richtext.test.ts` — Node.js 内置测试（`node:test`，10 个用例）
- `examples/demo.ts` — 可运行样例（4 个并发场景）

## 运行

无需安装依赖（Node ≥ 22.6 原生剥离类型标注；开发环境为 Node v23.9）：

```bash
npm test       # node --test "test/*.test.ts"
npm run demo   # node examples/demo.ts
```

## 运行样例输出（`npm run demo`）

```
=== 场景 1（验收）: 'ABC' 并发加粗 'AB' + 下划线 'BC' ===
  alice 副本:
    [0, 1) "A"  ->  bold
    [1, 2) "B"  ->  bold, underline
    [2, 3) "C"  ->  underline
  bob 副本:
    [0, 1) "A"  ->  bold
    [1, 2) "B"  ->  bold, underline
    [2, 3) "C"  ->  underline
  两个副本一致: true

=== 场景 2: 并发冲突 —— A 加粗 vs B 取消加粗（同区间） ===
  合并结果（lamport 相同，actor 'bob' > 'alice'，加粗获胜）:
    [0, 3) "ABC"  ->  bold
  因果更晚的取消加粗获胜:
    [0, 3) "ABC"  ->  (无格式)

=== 场景 3: 重叠超链接按 LWW 逐字符裁决 ===
  合并结果:
    [0, 1) "A"  ->  link=https://a.example
    [1, 3) "BC"  ->  link=https://b.example

=== 场景 4: 边界扩展 —— 在加粗区间末尾键入继承格式 ===
  键入后文本 "YABXC":
    [0, 1) "Y"  ->  (无格式)
    [1, 4) "ABX"  ->  bold
    [4, 5) "C"  ->  (无格式)
```

## 测试结果（`npm test`）

```
✔ 验收: 'ABC' 上并发加粗 'AB' 与下划线 'BC'，合并后无格式断裂
✔ 边界因果: 因果上后发生的取消加粗覆盖先前的加粗
✔ 边界因果: 并发的加粗与取消加粗按 (lamport, actor) 确定性裁决且收敛
✔ 边界因果: 部分重叠的取消加粗与并发斜体互不干扰
✔ 超链接冲突: 重叠区间按 LWW 逐字符裁决
✔ 收敛性: 操作乱序 + 重复应用后结果一致（交换律/结合律/幂等）
✔ 边界扩展: 末尾边界键入继承格式，起始边界不继承
✔ 并发插入: 同一位置并发插入按字符 ID 确定性排序
✔ 删除: 删除格式区间内的字符后区间收缩且不留空洞
✔ merge: 两个副本直接互合并与操作传输等价
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

## API 速览

```ts
import { RichText } from "./src/richtext.ts";

const alice = RichText.from("ABC", "alice");
const bob = RichText.fromOps("bob", alice.getOps()); // 派生副本

alice.format(0, 2, "bold", true);        // 'AB' 加粗
bob.format(1, 2, "underline", true);     // 'BC' 下划线（并发）
alice.format(0, 1, "bold", null);        // 取消 'A' 的加粗
bob.format(0, 2, "link", "https://x");   // 超链接（值为字符串）

alice.merge(bob);                        // 或 alice.apply(bob.getOps())
alice.getSpans();
// [ { start: 0, end: 1, text: "A", marks: { link: "https://x" } },
//   { start: 1, end: 2, text: "B", marks: { bold: true, link: "https://x", underline: true } },
//   { start: 2, end: 3, text: "C", marks: { underline: true } } ]
```
