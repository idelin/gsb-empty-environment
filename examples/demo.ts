import { AnnotatedDocument } from '../src/document.ts';

function show(doc: AnnotatedDocument, label: string): void {
  const anns = doc
    .getAnnotations()
    .map((a) => `#${a.id} [${a.start},${a.end}) ${a.startAffinity}/${a.endAffinity} -> ${JSON.stringify(doc.text.slice(a.start, a.end))}`)
    .join('\n    ');
  console.log(`${label}\n  文本: ${JSON.stringify(doc.text)}\n  批注:\n    ${anns || '(无)'}\n`);
}

// 1. 端点粘附：[2,4) 左端插字
const inward = new AnnotatedDocument('abcdef');
inward.addAnnotation(2, 4, 'inward', 'inward');
inward.applyEdit({ id: 1, start: 2, end: 2, text: 'XY' });
show(inward, '1a. [2,4) 左端插字 "XY"，左端向内 -> 不纳入');

const outward = new AnnotatedDocument('abcdef');
outward.addAnnotation(2, 4, 'outward', 'inward');
outward.applyEdit({ id: 1, start: 2, end: 2, text: 'XY' });
show(outward, '1b. [2,4) 左端插字 "XY"，左端向外 -> 纳入');

// 2. 删除全部覆盖内容 -> 零宽锚点
const anchor = new AnnotatedDocument('abcdef');
anchor.addAnnotation(2, 4);
anchor.applyEdit({ id: 1, start: 2, end: 4, text: '' });
show(anchor, '2. 删除批注全部内容 -> 保留零宽锚点');

// 3. 批量同位置编辑按操作ID排序
const batch = new AnnotatedDocument('ac');
batch.applyBatch([
  { id: 3, start: 1, end: 1, text: '3' },
  { id: 1, start: 1, end: 1, text: '1' },
  { id: 2, start: 1, end: 1, text: '2' },
]);
show(batch, '3. 同位置插入操作ID 3/1/2 -> 文本按 1,2,3 排序');

// 4. UTF-16 与代理对
const emoji = new AnnotatedDocument('a😀b');
emoji.addAnnotation(1, 3); // 覆盖 "😀"（占 2 个 UTF-16 偏移）
emoji.applyEdit({ id: 1, start: 2, end: 2, text: 'X' }); // 代理对中间插入 -> 对齐到对前
show(emoji, '4. 代理对中间插入自动对齐，"😀" 批注保持完整');

// 5. CRLF 文档
const crlf = new AnnotatedDocument('one\r\ntwo\r\nthree');
crlf.addAnnotation(5, 8); // "two"
crlf.applyEdit({ id: 1, start: 0, end: 0, text: 'zero\r\n' });
show(crlf, '5. CRLF 文档头部插入一行，"two" 批注平移');

// 6. 撤销恢复原锚点
const undoable = new AnnotatedDocument('abcdef');
undoable.addAnnotation(2, 4, 'outward', 'outward');
undoable.applyEdit({ id: 1, start: 2, end: 3, text: 'LONG' });
show(undoable, '6a. 编辑后（批注随替换变化）');
undoable.undo();
show(undoable, '6b. undo 后恢复原文本与原锚点 [2,4)');
