import { IncrementalHighlighter, type Token } from '../src/highlighter.ts';

function render(h: IncrementalHighlighter, title: string): void {
  console.log(`\n=== ${title} ===`);
  const text = h.getText();
  const tokens = h.getTokens();
  const byLine = new Map<number, Token[]>();
  // 用换行切分仅用于展示
  let lineStart = 0;
  let lineNo = 0;
  const lineRanges: Array<[number, number]> = [];
  for (let i = 0; i <= text.length; i++) {
    if (i === text.length || text[i] === '\n') {
      lineRanges.push([lineStart, i]);
      lineStart = i + 1;
      lineNo++;
    }
  }
  for (const t of tokens) {
    const ln = lineRanges.findIndex(([s, e]) => t.start >= s && t.start <= e);
    if (!byLine.has(ln)) byLine.set(ln, []);
    byLine.get(ln)!.push(t);
  }
  lineRanges.forEach(([s, e], ln) => {
    const content = text.slice(s, e).replace(/\r$/, '');
    const marks = (byLine.get(ln) ?? [])
      .map((t) => `    [${t.start},${t.end}) ${t.type.padEnd(10)} "${text.slice(t.start, t.end)}"`)
      .join('\n');
    console.log(`L${ln}: ${content}`);
    if (marks) console.log(marks);
  });
}

const src = [
  'let name = "pair\\"wise"   // 转义引号不结束字符串',
  'let total = 0',
  '/* 块注释开始',
  'let hidden = 1',
  '注释结束 */ let shown = 2',
  'let emoji😀id = 3',
].join('\n');

const h = new IncrementalHighlighter(src);
render(h, '初始高亮（UTF-16 偏移）');

// 1) 删除早期注释终结符 "*/" => 后续多行变为注释
const closeOff = src.indexOf('*/');
let r = h.applyEdit(closeOff, 2, '');
console.log(`\n>>> 删除第 4 行的 "*/"，失效行: [${r.invalidatedLines.from}, ${r.invalidatedLines.to}]`);
render(h, '删除 */ 后：后续行被重新着色为注释');

// 2) 恢复 "*/"
r = h.applyEdit(closeOff, 0, '*/');
console.log(`\n>>> 恢复 "*/"，失效行: [${r.invalidatedLines.from}, ${r.invalidatedLines.to}]`);

// 3) 普通标识符内改一字 => 只失效当前行
const idOff = h.getText().indexOf('total') + 2;
r = h.applyEdit(idOff, 1, 'X');
console.log(`\n>>> 标识符 total 内改一字，失效行: [${r.invalidatedLines.from}, ${r.invalidatedLines.to}]（后文不重算）`);

// 4) CRLF 文档
const crlf = new IncrementalHighlighter('let a = 1\r\n/* c\r\nx */\r\nlet b = 2');
render(crlf, 'CRLF 文档高亮');
