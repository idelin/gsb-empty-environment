import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PixelEditor } from '../src/editor.ts';
import { RGBAImage, type Color } from '../src/image.ts';

const RED: Color = [255, 0, 0, 255];
const BLUE: Color = [0, 0, 255, 255];
const GREEN: Color = [0, 255, 0, 255];
const BLANK: Color = [0, 0, 0, 0];

function hLine(x0: number, x1: number, y: number): [number, number][] {
  const pts: [number, number][] = [];
  for (let x = x0; x <= x1; x++) pts.push([x, y]);
  return pts;
}
function vLine(y0: number, y1: number, x: number): [number, number][] {
  const pts: [number, number][] = [];
  for (let y = y0; y <= y1; y++) pts.push([x, y]);
  return pts;
}

test('重叠笔触：撤销一个只恢复其前态，另一笔触不受影响', () => {
  const ed = new PixelEditor(16, 16);
  ed.stroke(hLine(2, 13, 8), RED, 'A-horizontal'); // 横笔
  ed.stroke(vLine(2, 13, 8), BLUE, 'B-vertical'); // 竖笔，与 A 交于 (8,8)

  assert.deepEqual(ed.image.getPixel(8, 8), BLUE); // 交叠处被 B 覆盖

  ed.undo(); // 撤销 B
  assert.deepEqual(ed.image.getPixel(8, 8), RED, '交叠点恢复为 B 之前的状态（A 的红色）');
  assert.deepEqual(ed.image.getPixel(8, 2), BLANK, 'B 独有的点被清除');
  assert.deepEqual(ed.image.getPixel(2, 8), RED, 'A 独有的点不受影响');

  ed.undo(); // 撤销 A
  assert.deepEqual(ed.image.getPixel(8, 8), BLANK);
  assert.deepEqual(ed.image.getPixel(2, 8), BLANK);
  assert.ok(ed.image.equals(new RGBAImage(16, 16)), '全部撤销后回到空图');

  ed.redo(); // 重做 A
  ed.redo(); // 重做 B
  assert.deepEqual(ed.image.getPixel(8, 8), BLUE, '逆序恢复后再正序重做，交叠处仍是 B');
});

test('裁剪再撤销：恢复被裁像素与原始尺寸', () => {
  const ed = new PixelEditor(8, 8);
  // 每个像素编码自身坐标，便于校验
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) ed.image.setPixel(x, y, [x * 30, y * 30, 7, 255]);
  }
  const original = ed.image.clone();

  ed.crop(2, 2, 4, 4);
  assert.equal(ed.image.width, 4);
  assert.equal(ed.image.height, 4);
  assert.deepEqual(ed.image.getPixel(0, 0), [60, 60, 7, 255], '裁剪后 (0,0) 对应原图 (2,2)');

  ed.undo();
  assert.equal(ed.image.width, 8);
  assert.equal(ed.image.height, 8);
  assert.ok(ed.image.equals(original), '撤销裁剪后所有被裁像素完整恢复');
});

test('元数据尺寸变化与像素编辑在同一事务链中交错撤销', () => {
  const ed = new PixelEditor(8, 8);
  ed.fillRect(0, 0, 8, 8, GREEN, 'bg');
  const full = ed.image.clone();
  ed.crop(1, 1, 4, 4, 'crop');
  ed.stroke([[0, 0], [1, 1]], RED, 'mark');

  ed.undo(); // 撤销 mark：仍是 4x4
  assert.equal(ed.image.width, 4);
  assert.deepEqual(ed.image.getPixel(0, 0), GREEN);

  ed.undo(); // 撤销 crop：恢复 8x8 全图
  assert.equal(ed.image.width, 8);
  assert.ok(ed.image.equals(full));

  ed.undo(); // 撤销 bg
  assert.ok(ed.image.equals(new RGBAImage(8, 8)));
});

test('分支撤销：undo 后新编辑产生分支，可选择分支重做', () => {
  const ed = new PixelEditor(4, 4);
  ed.stroke([[0, 0]], RED, 'e1');
  const e2 = ed.stroke([[1, 1]], GREEN, 'e2');
  ed.undo(); // 回到 e1
  const e3 = ed.stroke([[2, 2]], BLUE, 'e3'); // 在 e1 上开新分支

  // 当前路径是 e1 -> e3
  assert.deepEqual(ed.image.getPixel(2, 2), BLUE);
  assert.deepEqual(ed.image.getPixel(1, 1), BLANK);

  ed.undo(); // 回到 e1，两个分支可选
  const branches = ed.branches();
  assert.equal(branches.length, 2);
  assert.deepEqual(branches.map((b) => b.label).sort(), ['e2', 'e3']);

  ed.redo(e2.id); // 显式选择旧分支 e2
  assert.deepEqual(ed.image.getPixel(1, 1), GREEN);
  assert.deepEqual(ed.image.getPixel(2, 2), BLANK);

  ed.undo();
  ed.redo(); // 默认走最新分支 e3
  assert.deepEqual(ed.image.getPixel(2, 2), BLUE);
  assert.deepEqual(ed.image.getPixel(1, 1), BLANK);
});

test('历史淘汰：超限后当前图与无历史参考完全一致，且逐步撤销始终自洽', () => {
  const W = 16, H = 16, OPS = 60;
  const ed = new PixelEditor(W, H, { maxTransactions: 8 });
  const ref = new RGBAImage(W, H); // 无历史参考图

  let seed = 1234;
  const rand = () => {
    seed = (seed * 1664525 + 1013904223) >>> 0;
    return seed;
  };
  for (let i = 0; i < OPS; i++) {
    const x = rand() % W, y = rand() % H;
    const color: Color = [rand() % 256, rand() % 256, rand() % 256, 255];
    ed.fillRect(x, y, 2, 2, color, `op${i}`);
    for (let dy = 0; dy < 2; dy++)
      for (let dx = 0; dx < 2; dx++)
        if (x + dx < W && y + dy < H) ref.setPixel(x + dx, y + dy, color);
  }

  assert.ok(ed.image.equals(ref), '淘汰后当前图与参考一致');
  assert.ok(ed.verifyAgainstReplay(), '从基线重放与当前图一致');
  const stats = ed.stats();
  assert.ok(stats.liveTransactions <= 8, `活事务数 ${stats.liveTransactions} 应 <= 8`);
  assert.ok(stats.evictedIntoBase > 0, '应发生了前滚合并淘汰');
  assert.ok(stats.undoDepth <= 8);

  // 逐步撤销到底，每步都与基线重放自洽
  while (ed.undo()) {
    assert.ok(ed.verifyAgainstReplay(), '撤销过程中历史始终自洽');
  }
  assert.ok(ed.verifyAgainstReplay());
  assert.equal(ed.undo(), false, '到达（压缩后的）基线后无法继续撤销');
});

test('内存压缩：纯色大矩形的差量负载远小于原始像素', () => {
  const ed = new PixelEditor(64, 64);
  ed.fillRect(0, 0, 64, 64, RED, 'big-rect'); // 64*64*4 = 16384 原始字节
  const stats = ed.stats();
  assert.ok(
    stats.payloadBytes < 16384 / 20,
    `压缩后负载 ${stats.payloadBytes}B 应远小于原始 16384B`,
  );
});
