# chunked-sentence-segmenter

流式（分块）断句库，TypeScript 实现。断句规则由三部分组成：

- **缩写词典**：句点紧跟的 token 命中词典（如 `Dr.`、`U.S.A.`）时不构成边界；词典按版本切换。
- **引号配对**：维护引号栈（`"`、`'`、`()`、`[]`），终止标点后的连续闭引号归属当前句子；字母间的撇号（`It's`）不算引号。
- **终止标点**：`.` `!` `?`（可连写，如 `?!`、`...`）后随空白或输入结束时形成边界。

## 分块断句与未决区

`feed(chunk)` 只提交边界已完全确定的句子。以下情况保留在未决区（pending region），等待后续输入：

- chunk 末尾的终止标点（下一个字符可能是非空白，如 `3.14`、`a.b`）；
- chunk 末尾可能继续生长的缩写前缀（如 `U.` 可能是 `U.S.A.` 的一部分）；
- 尚未出现终止标点的句子。

`flush()` 表示输入结束，提交全部未决内容（包括没有终止标点的尾句）。

已提交句子的来源区间（`start`/`end`，针对拼接后的完整输入的全局偏移）一经返回即稳定：切换词典版本只影响未决区，不会改写历史。

## 用法

```ts
import { SentenceSegmenter, segmentAll } from './src/index.ts';

const options = {
  dictionaries: {
    v1: ['Dr.', 'Corp.', 'U.S.', 'U.S.A.'],
    v2: ['Dr.', 'U.S.', 'U.S.A.'],   // v2 移除了 Corp.
  },
  version: 'v1',
};

const seg = new SentenceSegmenter(options);
seg.feed('Alpha Corp. ');        // [] —— Corp. 是缩写，保持未决
seg.setDictionaryVersion('v2');  // 只影响未决区
seg.feed('Beta.');               // => ['Alpha Corp.']
seg.flush();                     // => ['Beta.']

segmentAll('Dr. Smith left. OK!', options); // 全量状态机（批式参照）
```

## 测试

需要 Node ≥ 22.14（22.x 需加 `--experimental-strip-types`；23.6+ 原生支持）：

```sh
node --test test/segmenter.test.ts   # 或 npm test
```

测试覆盖：连续/嵌套引号、词典前缀重叠项（`U.S.` vs `U.S.A.`）、空段与空 chunk、逐字符及随机分块输入与全量状态机结果对照、词典版本切换与已提交区间稳定性。
