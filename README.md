# selection-mapping

多选择区在文档版本间的双向映射（TypeScript，零依赖，Node.js 内置测试）。

## 设计

- **`src/mapping.ts` — 结构化映射。** `Mapping` 是有序编辑步骤（UTF-16 码元）
  的列表，而不是最终偏移：`compose` 连接步骤、`invert` 逆序并交换
  deleted/inserted，满足 `(a∘b)⁻¹ = b⁻¹∘a⁻¹`。`mapOffset` 正向映射；
  `mapOffsetBack` 逆向映射并对丢失信息（删除塌缩点、无来源的插入文本）
  返回 `ambiguous` 与 `sources`。
- **`src/document.ts` — 逐字符身份模型。** 每个原子是一个码点（1–2 个
  UTF-16 码元）并携带全局唯一 id。公开偏移一律为 UTF-16；删除向外吸附、
  插入吸附到代理对边界，绝不切开代理对；`\r`、`\n`（含 CRLF）是普通原子。
- **`src/selections.ts` — 多选择区。** 锚端与活动端分别保存（偏移 +
  零宽粘附 affinity），方向显式存储，零宽时映射后保持不变。删除使选择区
  相触即合并：保留最小 id 的主选区身份与方向，被吞并的选择区以来源记录
  （id、方向、端点粘附，取合并前所在版本的坐标）保存在 `sources` 中；
  `mapBackward` 对丢失信息输出 `merged-sources` / `offset` 两类歧义。
- **`src/history.ts` — 版本历史。** 每个版本保存选择区快照与相邻版本间的
  结构化映射。`checkout`（分支撤销）精确恢复合并前的多个选择区；
  `reclaim` + `compress` 回收死版本并把跨缺口的映射组合起来，存活版本的
  文本 / 选择区 / 版本间映射查询结果不变。

## 运行

```sh
npm test    # node --test test/*.test.ts（Node ≥ 22.6，原生类型擦除）
npm run demo
```

## 样例输出（`npm run demo`）

```
=== 1. Two selections + a deletion that merges them ===
  v0 text: "aa111bb222cc"
  v0 selections: #1 ◀ anchor=5(after) active=1(before)  |  #2 ▶ anchor=7(after) active=10(before)

  edit: delete [4,8) -> "aa1122cc"
  v1 selections: #1 ◀ anchor=6(before) active=1(before) sources=[#2]
  merge: into=#1 range=[1,6) lost=#2 forward [7,10]

=== 2. Reverse mapping: ambiguity for lost information ===
  mapped back to v0: #1 ◀ anchor=10(before) active=1(before) sources=[#2]
  ambiguity(merged-sources): selection #1 swallowed #2 forward anchor=7 active=10

=== 3. Branch undo restores the original selections ===
  after checkout(v0): #1 ◀ anchor=5(after) active=1(before)  |  #2 ▶ anchor=7(after) active=10(before)

=== 4. Two cursors crushed by one deletion ===
  u0 cursors: #1 ▶ anchor=2(after) active=2(after)  |  #2 ▶ anchor=4(after) active=4(after)
  edit: delete [2,4) -> "aabb"
  u1 cursors: #1 ▶ anchor=2(after) active=2(after) sources=[#2]
  undo -> u0: #1 ▶ anchor=2(after) active=2(after)  |  #2 ▶ anchor=4(after) active=4(after)

=== 5. History compression keeps live-version queries intact ===
  w0 text: "hello 😀 world\r\nsecond line" (len 27 UTF-16 units)
  w3 text: ">> hello  world second line"
  versions kept after compress: 2 (reclaimed w1, w2)
  mapping w0->w3 identical before/after compression: true
  live selections at w3: #1 ▶ anchor=9(after) active=9(before)  |  #2 ▶ anchor=16(after) active=16(after)
```

## 测试（`npm test`，29 项全部通过）

- `test/mapping.test.ts` — 偏移映射规则、组合/反向律、往返一致性、歧义来源
  （删除 / 插入）、代理对吸附、LF 与 CRLF。
- `test/selections.test.ts` — 锚端/活动端分离存储、反向选择经前方插入方向不变、
  零宽粘附、双光标被一次删除压到同点只留一个可见光标、删除合并保留主选区身份、
  逆向歧义输出、分支撤销恢复。
- `test/history.test.ts` — 压缩组合映射且存活版本查询不变、回收版本拒绝快照查询、
  分支撤销截断重做尾、随机编辑下压缩前后结果一致。
- `test/fuzz.test.ts` — 逐字符身份模型（oracle）对拍：400 轮随机编辑 × 随机选择区
  （字母表含 emoji / `\r` / `\n`），2000+ 端点比对；组合与反向律随机验证；
  合并不变量（不相触、主 id 最小、来源记录完整、id 不凭空消失）。

```
ℹ tests 29
ℹ pass 29
ℹ fail 0
```
