# chunked-search

分块文本上的跨块字面搜索 + 增量匹配索引（TypeScript，Node.js 内置测试运行器，零依赖）。

- 文本按块存储（`ChunkedText`），公开偏移一律为 **UTF-16 code unit 偏移**。
- 跨块字面搜索：重叠匹配、大小写精确（可选不敏感）、从指定偏移开始。
- 多模式匹配索引 `MatchIndex`：编辑后**差量更新**匹配集合与旧版本游标。
- 可暂停枚举 `MatchCursor`：多模式合并、含重叠结果，随时暂停/恢复。
- 流式 KMP：任何时刻只持有 O(模式长) 的失配表与 O(1) 文本字符，
  **模式长于分块也不需要拼接全文**；`charsRead` 统计实际重读的字符数。

## 运行

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

（Node ≥ 22.6，直接以 type-stripping 运行 .ts，无需编译。）

## 编辑影响窗口的推导

设编辑把旧文本区间 `[s, e)` 替换为长度 `L` 的新串，`delta = L - (e - s)`，
模式长 `m`。起始偏移为 `p` 的匹配覆盖区间 `[p, p+m)`：

1. `p < s - (m-1)`：`p+m-1 < s`，匹配整体在编辑点之前 → 内容与偏移均不变，**保留**。
2. `s-(m-1) <= p < e`：`[p, p+m)` 与被替换区间 `[s, e)` 相交 → 匹配可能失效，**丢弃**。
3. `p >= e`：匹配整体在编辑之后，内容不变 → 偏移 **平移 delta**。

新匹配只可能起始自 `[s-(m-1), s+L)`（起点 `< s-(m-1)` 则整体未变，早已知；
起点 `>= s+L` 等价于旧文本中 `>= e` 的匹配平移而来，也已知）。
因此索引只需在新文本中重扫窗口

```
lo = max(0, s - (m - 1))
hi = s + L - 1            (起始偏移，含)
读取字符区间 [lo, min(len, hi + m))，大小 <= L + 2m - 2
```

与文本总长无关。多模式时按各模式各自的 `m` 分别重扫（空模式按定义直接重建，
不读文本）。实测：5000 字符文本中部删除 3 字符，重读 ≤ 32 字符；
随机编辑对拍 480 次，平均每次重读约 34 字符。

## 空模式语义

空模式在每个位置匹配：全文搜索返回 `0..length` 共 `length+1` 个偏移；
指定 `from` 时返回 `from..length`。索引中的空模式在编辑后按同一规则重建。

## UTF-16 / 代理对 / 换行约定

- 所有公开偏移为 UTF-16 code unit 偏移（与 JS 字符串下标一致）。
- 分块边界永不切开代理对（chunkSize=1 时该块扩展为 2 个 code unit）；
  会切开代理对的编辑区间抛出 `RangeError`；落在代理对中间的 `from` 偏移
  自动前移到代理对之后。
- `\n` 与 `\r\n` 均为普通 code unit，`\r\n` 跨块（`\r` 在块尾、`\n` 在块首）
  照常匹配，测试两种换行均覆盖。

## 游标语义（差量更新）

`index.query(patternIds, from)` 返回可暂停游标，按 `(偏移, 模式序)` 归并
多模式的全部匹配（含重叠）。编辑发生时：

- 游标位置在编辑区间之后 → 平移 `delta`，已发射的匹配不重复发射；
- 游标位置落在被替换区间内 → 回退到影响窗口起点，可能重发窗口内更新后的匹配；
- 游标位置在编辑点之前 → 不受影响。

## 运行样例（`npm run demo`）

```
"aaaa" / "aa" (chunkSize 2): [ 0, 1, 2 ]
"abcde" across 3 chunks: [ 1 ]
"aa" case-exact: [ 2 ]
"aa" case-insensitive: [ 0, 1, 2 ]
"aa" from offset 2: [ 2 ]
empty pattern in "abc": [ 0, 1, 2, 3 ]
cursor (paused after 2):
   { patternId: 'the', start: 0, end: 3 } { patternId: 'o', start: 12, end: 13 }
  ... paused; editing text ...
  resumed after edit:
   { patternId: 'o', start: 16, end: 17 }
   { patternId: 'o', start: 25, end: 26 }
   { patternId: 'the', start: 30, end: 33 }
   { patternId: 'o', start: 40, end: 41 }
matches("o") after edits: [ 6, 14, 23, 38 ]
chars re-read for last edit: 25
"aa" in "aabaa": [ 0, 3 ]
"aa" after deleting boundary char: [ 0, 1, 2 ]
"😀" offsets (UTF-16): [ 1, 6 ]
"\r\n" offset: [ 3 ]
edit splitting a surrogate pair: edit range splits a surrogate pair
```

## 测试结果（`npm test`）

23 个测试全部通过，包括：

- `"aaaa"` 中搜 `"aa"` 在 chunkSize 1/2/3/4/10 下均返回 `[0,1,2]`；
- 长 5 的模式横跨 3 个块（chunkSize 2）命中；
- 删除块边界字符后产生的新匹配（`"aabaa"→"aaaa"`，`"aa"` 得到 `[0,1,2]`）；
- 自重叠模式（`aba`/`aa`）、代理对（不切块、编辑拒绝、偏移钳制）、
  LF 与 CRLF（含 `\r\n` 跨块）、空模式、大小写精确/不敏感、指定偏移起始；
- 随机编辑对拍：60 段随机文本 × 8 次随机编辑（含 emoji/CRLF 字母表），
  每次编辑后 8 个模式的索引结果与朴素搜索逐一比对，并断言实际重读字符数
  不超过编辑影响窗口（与文本总长无关）。

```
random cross-check: 480 edits, avg re-read 34.2 chars/edit, worst allowed window 100
ℹ tests 23
ℹ pass 23
ℹ fail 0
```

## 文件

- `src/chunked-text.ts` — 分块存储、局部编辑、读取计数
- `src/search.ts` — 流式 KMP 跨块搜索（`findAll` / `searchRange`）
- `src/match-index.ts` — 多模式增量索引 `MatchIndex` 与游标 `MatchCursor`
- `test/` — node:test 测试；`examples/demo.ts` — 运行样例
