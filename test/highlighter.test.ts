import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalHighlighter,
  STATE_IN_BLOCK_COMMENT,
  STATE_NORMAL,
  lexLine,
  splitLines,
  type Token,
} from '../src/highlighter.ts';

function types(tokens: Token[]): string[] {
  return tokens.map((t) => t.type);
}

test('基础词法：关键字、标识符、数字、字符串、运算符', () => {
  const h = new IncrementalHighlighter('let total = 42 + "hi"');
  const tokens = h.getTokens();
  assert.deepEqual(types(tokens), ['keyword', 'identifier', 'operator', 'number', 'operator', 'string']);
  assert.deepEqual(
    tokens.map((t) => [t.start, t.end]),
    [[0, 3], [4, 9], [10, 11], [12, 14], [15, 16], [17, 21]],
  );
});

test('转义引号不结束字符串', () => {
  const h = new IncrementalHighlighter('let s = "a\\"b" + tail');
  const tokens = h.getTokens();
  const str = tokens.find((t) => t.type === 'string')!;
  assert.equal(h.getText().slice(str.start, str.end), '"a\\"b"');
  // 字符串之后的内容仍是普通 token，而非字符串的延续
  assert.deepEqual(types(tokens), ['keyword', 'identifier', 'operator', 'string', 'operator', 'identifier']);
});

test('连续转义：\\\\ 之后的引号应当结束字符串', () => {
  const h = new IncrementalHighlighter('"a\\\\" rest');
  const tokens = h.getTokens();
  assert.equal(tokens[0].type, 'string');
  assert.equal(h.getText().slice(tokens[0].start, tokens[0].end), '"a\\\\"');
  assert.equal(tokens[1].type, 'identifier');
});

test('跨行块注释：注释内代码被着为 comment', () => {
  const src = 'let a = 1\n/* comment\nlet b = 2\nend */ let c = 3';
  const h = new IncrementalHighlighter(src);
  assert.equal(h.getEntryState(1), STATE_NORMAL);
  assert.equal(h.getEntryState(2), STATE_IN_BLOCK_COMMENT);
  assert.equal(h.getEntryState(3), STATE_IN_BLOCK_COMMENT);
  const line2 = h.getTokensForLine(2);
  assert.deepEqual(types(line2), ['comment']); // let b = 2 整行是注释
  const line3 = h.getTokensForLine(3);
  assert.deepEqual(types(line3), ['comment', 'keyword', 'identifier', 'operator', 'number']);
});

test('删除早期注释终结符 */：后续多行颜色变化，失效行传播到状态稳定处', () => {
  const src = 'let a = 1\n/* c1\nlet b = 2\nend */\nlet c = 3\nlet d = 4';
  const h = new IncrementalHighlighter(src);
  const before = h.getTokens();

  // 删除第 3 行的 "*/"（偏移：逐行长度 10+1 + 6+1 + 10+1 + 6 = 35，"*/" 在 "end " 之后）
  const closeOffset = src.indexOf('*/');
  const result = h.applyEdit(closeOffset, 2, '');

  // 从第 3 行起直到文末都变成注释 => 失效行覆盖 3..5
  assert.deepEqual(result.invalidatedLines, { from: 3, to: 5 });
  assert.equal(h.getEntryState(4), STATE_IN_BLOCK_COMMENT);
  assert.equal(h.getEntryState(5), STATE_IN_BLOCK_COMMENT);
  assert.deepEqual(types(h.getTokensForLine(4)), ['comment']);
  assert.deepEqual(types(h.getTokensForLine(5)), ['comment']);

  const after = h.getTokens();
  assert.notDeepEqual(
    after.filter((t) => t.start >= closeOffset),
    before.filter((t) => t.start >= closeOffset),
  );
});

test('重新插入 */ 后颜色恢复，且只重扫必要行', () => {
  const src = '/* a\nb */\nlet x = 1';
  const h = new IncrementalHighlighter(src);
  const off = src.indexOf('*/');
  h.applyEdit(off, 2, '');
  assert.equal(h.getEntryState(2), STATE_IN_BLOCK_COMMENT);
  const r = h.applyEdit(off, 0, '*/');
  assert.equal(h.getEntryState(2), STATE_NORMAL);
  assert.deepEqual(types(h.getTokensForLine(2)), ['keyword', 'identifier', 'operator', 'number']);
  assert.equal(r.invalidatedLines.from, 1);
});

test('普通标识符内改一字：不重算无关后文', () => {
  const lines = [];
  for (let i = 0; i < 50; i++) lines.push(`let value${i} = ${i}`);
  const src = lines.join('\n');
  const h = new IncrementalHighlighter(src);

  // 第 10 行标识符中间改一个字符
  const lineStart = src.split('\n').slice(0, 10).join('\n').length + 1;
  const offset = lineStart + 6; // "value10" 内部
  const result = h.applyEdit(offset, 1, 'X');

  // 只失效第 10 行，后续 49 行全部复用缓存
  assert.deepEqual(result.invalidatedLines, { from: 10, to: 10 });

  // 后文 token 内容不变（绝对偏移也未变，因为编辑等长）
  const tokens = h.getTokens();
  const line11Start = src.indexOf('let value11');
  const later = tokens.filter((t) => t.start >= line11Start);
  assert.equal(later[0].type, 'keyword');
  assert.equal(src.slice(later[1].start, later[1].end).startsWith('value11'), true);
});

test('插入 /* 使后续行变注释，直到遇到 */ 稳定', () => {
  const src = 'let a = 1\nlet b = 2\n/* x */\nlet c = 3';
  const h = new IncrementalHighlighter(src);
  // 在第 0 行行首插入 "/*"
  const result = h.applyEdit(0, 0, '/*');
  // 第 0..2 行被重扫（第 2 行原本的 /* x */ 自带终结符，状态在此稳定）
  assert.deepEqual(result.invalidatedLines, { from: 0, to: 2 });
  assert.equal(h.getEntryState(3), STATE_NORMAL);
  assert.deepEqual(types(h.getTokensForLine(3)), ['keyword', 'identifier', 'operator', 'number']);
});

test('CRLF 与 LF 偏移一致（UTF-16 绝对偏移）', () => {
  const lf = 'let a = 1\nlet b = 2';
  const crlf = 'let a = 1\r\nlet b = 2';
  const hLf = new IncrementalHighlighter(lf);
  const hCrlf = new IncrementalHighlighter(crlf);

  const tLf = hLf.getTokensForLine(1);
  const tCrlf = hCrlf.getTokensForLine(1);
  assert.deepEqual(types(tLf), types(tCrlf));
  // CRLF 多一个 \r，第二行整体偏移 +1
  assert.equal(tCrlf[0].start, tLf[0].start + 1);
  // 用偏移取回原文验证
  assert.equal(crlf.slice(tCrlf[0].start, tCrlf[0].end), 'let');
  assert.equal(lf.slice(tLf[0].start, tLf[0].end), 'let');
});

test('splitLines 覆盖 LF / CRLF / 孤立 CR / 末尾空行', () => {
  const { lines, starts } = splitLines('a\r\nb\nc\rd\n');
  assert.deepEqual(lines, ['a', 'b', 'c', 'd', '']);
  assert.deepEqual(starts, [0, 3, 5, 7, 9]);
});

test('代理对：emoji 标识符不被切开，偏移为 UTF-16 code unit', () => {
  const src = 'let a😀b = 1\nlet c = "x😀y"';
  const h = new IncrementalHighlighter(src);
  const tokens = h.getTokens();
  const ident = tokens[1];
  assert.equal(ident.type, 'identifier');
  // 😀 占 2 个 UTF-16 code unit
  assert.equal(src.slice(ident.start, ident.end), 'a😀b');
  assert.equal(ident.end - ident.start, 4);
  const str = tokens.find((t) => t.type === 'string')!;
  assert.equal(src.slice(str.start, str.end), '"x😀y"');
  // 所有 token 边界都不得落在代理对中间
  for (const t of tokens) {
    for (const off of [t.start, t.end]) {
      if (off > 0 && off < src.length) {
        const lead = src.charCodeAt(off - 1);
        const trail = src.charCodeAt(off);
        const splitsPair =
          lead >= 0xd800 && lead <= 0xdbff && trail >= 0xdc00 && trail <= 0xdfff;
        assert.equal(splitsPair, false, `offset ${off} splits surrogate pair`);
      }
    }
  }
});

test('applyEdit 自动对齐代理对边界', () => {
  const src = 'let 😀 = 1';
  const h = new IncrementalHighlighter(src);
  const emojiStart = src.indexOf('😀');
  // 在代理对中间插入，应被对齐到码点边界之前
  h.applyEdit(emojiStart + 1, 0, 'Z');
  const tokens = h.getTokens();
  const ident = tokens.find((t) => t.type === 'identifier')!;
  assert.equal(h.getText().slice(ident.start, ident.end), 'Z😀');
});

test('编辑导致行数变化后状态仍正确', () => {
  const src = '/* a */\nlet x = 1';
  const h = new IncrementalHighlighter(src);
  // 把 "*/" 换成 "*/\n/*"：第 0 行不变，新增一行并重新打开注释
  const off = src.indexOf('*/');
  const r = h.applyEdit(off, 2, '*/\n/*');
  assert.equal(h.lineCount, 3);
  assert.equal(h.getEntryState(2), STATE_IN_BLOCK_COMMENT);
  assert.deepEqual(types(h.getTokensForLine(2)), ['comment']);
  assert.deepEqual(r.invalidatedLines, { from: 1, to: 2 });
});

test('增量结果与全量重扫一致（多种编辑序列）', () => {
  const base = 'let a = "s\\"x"\n/* block\ncomment */\nlet b😀 = 2\n// tail';
  const edits: Array<[number, number, string]> = [
    [base.indexOf('*/'), 2, ''],           // 删掉终结符
    [base.indexOf('*/'), 0, '*/'],          // 恢复
    [0, 0, '/*'],                           // 开头打开注释
    [0, 2, ''],                             // 撤销
    [5, 1, 'Z'],                            // 标识符内改字
    [base.length, 0, '\nlet c = 3\r\nlet d = 4'], // 末尾追加（含 CRLF）
  ];
  const inc = new IncrementalHighlighter(base);
  for (const [start, del, ins] of edits) {
    inc.applyEdit(start, del, ins);
    const fresh = new IncrementalHighlighter(inc.getText());
    assert.deepEqual(inc.getTokens(), fresh.getTokens(), `mismatch after edit ${JSON.stringify(ins)}`);
  }
});

test('lexLine 字符串不跨行：未闭合字符串在行尾结束，下一行状态为 Normal', () => {
  const tokens: Token[] = [];
  const exit = lexLine('let s = "unterminated', STATE_NORMAL, tokens);
  assert.equal(exit, STATE_NORMAL);
  const str = tokens.find((t) => t.type === 'string')!;
  assert.equal(str.end, 'let s = "unterminated'.length);
});
