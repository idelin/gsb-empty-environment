/** 运行样例：重叠笔触、裁剪撤销、分支重做、历史淘汰与内存压缩。 */
import { PixelEditor } from '../src/editor.ts';
import { RGBAImage, type Color } from '../src/image.ts';

const RED: Color = [255, 0, 0, 255];
const BLUE: Color = [0, 0, 255, 255];
const GREEN: Color = [0, 255, 0, 255];

function render(img: RGBAImage): string {
  const glyph = (c: Color) =>
    c[3] === 0 ? '.' : c[0] > 200 ? 'R' : c[2] > 200 ? 'B' : c[1] > 200 ? 'G' : '#';
  const rows: string[] = [];
  for (let y = 0; y < img.height; y++) {
    let row = '';
    for (let x = 0; x < img.width; x++) row += glyph(img.getPixel(x, y));
    rows.push(row);
  }
  return rows.join('\n');
}

function show(title: string, img: RGBAImage) {
  console.log(`\n== ${title} (${img.width}x${img.height}) ==`);
  console.log(render(img));
}

// 1) 重叠笔触：撤销一个只恢复其前态
const ed = new PixelEditor(12, 12);
ed.stroke(Array.from({ length: 9 }, (_, i) => [i + 2, 6] as [number, number]), RED, 'A:横笔');
ed.stroke(Array.from({ length: 9 }, (_, i) => [6, i + 2] as [number, number]), BLUE, 'B:竖笔');
show('两个重叠笔触 A(红横) + B(蓝竖)', ed.image);
ed.undo();
show('撤销 B：交点恢复为 B 之前的红色，A 完好', ed.image);
ed.undo();
show('再撤销 A：回到空画布', ed.image);

// 2) 裁剪再撤销：恢复被裁像素
ed.fillRect(0, 0, 12, 12, GREEN, '铺底');
ed.crop(3, 3, 6, 6, '裁剪为 6x6');
show('裁剪后（尺寸变化已纳入事务）', ed.image);
ed.undo();
show('撤销裁剪：被裁像素全部恢复', ed.image);

// 3) 分支撤销
const ed2 = new PixelEditor(4, 4);
ed2.stroke([[0, 0]], RED, 'e1');
ed2.stroke([[1, 1]], GREEN, 'e2');
ed2.undo();
ed2.stroke([[2, 2]], BLUE, 'e3'); // 新分支
ed2.undo();
console.log('\n== 分支：e1 之后有两个可重做分支 ==');
console.log(ed2.branches().map((b) => `#${b.transactionId} ${b.label}`).join(' | '));
ed2.redo(); // 默认最新分支 e3
show('默认重做到最新分支 e3（蓝点）', ed2.image);
ed2.undo();
const e2id = ed2.branches().find((b) => b.label === 'e2')!.transactionId;
ed2.redo(e2id);
show('显式选择旧分支 e2（绿点）', ed2.image);

// 4) 历史淘汰 + 内存压缩
const ed3 = new PixelEditor(32, 32, { maxTransactions: 10 });
let seed = 7;
const rand = () => (seed = (seed * 1664525 + 1013904223) >>> 0);
for (let i = 0; i < 100; i++) {
  ed3.fillRect(rand() % 30, rand() % 30, 3, 3, [rand() % 256, rand() % 256, rand() % 256, 255], `op${i}`);
}
const s = ed3.stats();
console.log('\n== 100 次编辑，历史上限 10 ==');
console.log(`活事务: ${s.liveTransactions}  前滚合并: ${s.evictedIntoBase}  可撤销深度: ${s.undoDepth}`);
console.log(`差量负载: ${s.payloadBytes}B  基线快照: ${s.baseBytes}B（原始整图 ${32 * 32 * 4}B）`);
console.log(`淘汰后当前图与基线重放一致: ${ed3.verifyAgainstReplay()}`);
