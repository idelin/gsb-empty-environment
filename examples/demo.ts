import { RichTextDoc } from '../src/richtext.ts';

function sync(a: RichTextDoc, b: RichTextDoc): void {
  const opsA = [...a.getOps()];
  const opsB = [...b.getOps()];
  a.applyAll(opsB);
  b.applyAll(opsA);
}

function show(doc: RichTextDoc, title: string): void {
  console.log(`\n== ${title} ==`);
  const text = doc.getText();
  const formats = doc.getFormats();
  for (let i = 0; i < text.length; i += 1) {
    const marks = formats[i].length > 0 ? formats[i].join(' + ') : '(无格式)';
    console.log(`  ${text[i]}  →  ${marks}`);
  }
  const spans = doc.getSpans().map((s) => `${s.type}[${s.start},${s.end})`).join('  ');
  console.log(`  标注区间: ${spans || '(无)'}`);
}

console.log('场景 1：用户1 给 AB 加粗，用户2 并发给 BC 加下划线');
const alice = RichTextDoc.fromText('user1', 'ABC');
const bob = new RichTextDoc('user2');
bob.applyAll(alice.getOps());

alice.addMark('bold', 0, 2); // 用户1：AB 加粗
bob.addMark('underline', 1, 3); // 用户2 并发：BC 下划线
sync(alice, bob);

show(alice, '合并结果');
console.log(`  两副本收敛一致: ${alice.equals(bob)}`);

console.log('\n场景 2：初始 ABC 全部加粗；用户1 取消 AB 加粗，用户2 并发给 BC 再加粗');
const a2 = RichTextDoc.fromText('user1', 'ABC');
a2.addMark('bold', 0, 3);
const b2 = new RichTextDoc('user2');
b2.applyAll(a2.getOps());

a2.removeMark('bold', 0, 2); // 用户1：取消 AB 的加粗
b2.addMark('bold', 1, 3); // 用户2 并发：给 BC 再加粗（新标注）
sync(a2, b2);

show(a2, '合并结果（因果判定：并发新增标注胜出，A 不再加粗，B/C 保持加粗）');
console.log(`  两副本收敛一致: ${a2.equals(b2)}`);
