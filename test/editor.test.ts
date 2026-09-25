import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Editor } from '../src/editor.ts';
import { RgbaImage, type Color } from '../src/rgba-image.ts';
import { rleEncode, rleDecode } from '../src/rle.ts';

const T: Color = [0, 0, 0, 0];        // transparent
const R: Color = [255, 0, 0, 255];    // red
const B: Color = [0, 0, 255, 255];    // blue
const G: Color = [0, 200, 0, 255];    // green

function px(editor: Editor, x: number, y: number): Color {
  return editor.image.getPixel(x, y);
}

test('overlapping strokes: undo restores each stroke pre-state in reverse order', () => {
  const ed = new Editor(new RgbaImage(8, 8));
  ed.strokeRect(1, 3, 6, 1, R, 'A'); // horizontal row 3, cols 1..6
  ed.strokeRect(4, 1, 1, 6, B, 'B'); // vertical col 4, rows 1..6, overlaps at (4,3)

  assert.deepEqual(px(ed, 4, 3), B, 'overlap belongs to the later stroke');

  ed.undo(); // revert B only
  assert.deepEqual(px(ed, 4, 3), R, 'overlap returns to stroke A pre-state');
  assert.deepEqual(px(ed, 4, 1), T, 'B-only pixel restored to transparent');
  assert.deepEqual(px(ed, 2, 3), R, 'A-only pixel untouched by undo of B');

  ed.undo(); // revert A
  for (let y = 0; y < 8; y++)
    for (let x = 0; x < 8; x++)
      assert.deepEqual(px(ed, x, y), T, `pixel (${x},${y}) back to transparent`);

  ed.redo();
  ed.redo(); // replay both strokes
  assert.deepEqual(px(ed, 4, 3), B);
  assert.deepEqual(px(ed, 2, 3), R);
  assert.deepEqual(px(ed, 4, 5), B);
});

test('crop is a transaction: undo restores dimensions and cropped pixels', () => {
  const img = new RgbaImage(6, 4);
  for (let y = 0; y < 4; y++)
    for (let x = 0; x < 6; x++) img.setPixel(x, y, [x * 40, y * 60, 7, 255]);
  const original = img.clone();

  const ed = new Editor(img);
  ed.crop(1, 1, 3, 2);
  assert.equal(ed.image.width, 3);
  assert.equal(ed.image.height, 2);
  assert.deepEqual(px(ed, 0, 0), [40, 60, 7, 255], 'kept rect shifted to origin');
  const cropped = ed.image.clone();

  ed.undo();
  assert.equal(ed.image.width, 6);
  assert.equal(ed.image.height, 4);
  assert.ok(ed.image.equals(original), 'every cropped-away pixel restored');

  ed.redo();
  assert.ok(ed.image.equals(cropped), 'redo re-crops identically');
});

test('metadata size change joins a multi-op transaction and undoes atomically', () => {
  const ed = new Editor(new RgbaImage(5, 5));
  ed.strokeRect(0, 0, 1, 1, R, 'before');
  const beforeTxn = ed.image.clone();

  ed.beginTransaction();
  ed.strokeRect(2, 2, 2, 2, B);
  ed.crop(1, 1, 3, 3); // dimension change inside the same transaction
  assert.equal(ed.commitTransaction('stroke+crop'), true);

  assert.equal(ed.image.width, 3);
  ed.undo(); // single undo reverts the whole transaction
  assert.equal(ed.image.width, 5);
  assert.equal(ed.image.height, 5);
  assert.ok(ed.image.equals(beforeTxn), 'stroke and crop reverted together');
});

test('branching undo: new edit after undo forks history, redo picks branches', () => {
  const ed = new Editor(new RgbaImage(4, 1));
  ed.strokeRect(0, 0, 1, 1, R, 'A');
  ed.strokeRect(1, 0, 1, 1, B, 'B');
  ed.undo();                    // back to A
  ed.strokeRect(2, 0, 1, 1, G, 'C'); // forks a branch off A

  ed.undo(); // to A
  assert.deepEqual(ed.branches(), ['B', 'C']);

  ed.redo(); // follows the active branch (C)
  assert.deepEqual(px(ed, 2, 0), G);
  assert.deepEqual(px(ed, 1, 0), T, 'branch C does not contain B pixels');
  assert.equal(ed.label, 'C');

  ed.undo();
  ed.redo(0); // explicitly take branch B
  assert.deepEqual(px(ed, 1, 0), B);
  assert.deepEqual(px(ed, 2, 0), T);
  assert.equal(ed.label, 'B');
});

test('history eviction folds old deltas into the base without corrupting the image', () => {
  const ed = new Editor(new RgbaImage(10, 10), { maxDepth: 3, compressAfter: 1 });
  const expected = new RgbaImage(10, 10);
  for (let i = 0; i < 6; i++) {
    const c: Color = [i * 30 + 1, 5, 9, 255];
    ed.strokeRect(i, 0, 1, 1, c, `s${i}`);
    expected.setPixel(i, 0, c);
  }
  assert.ok(ed.depth <= 3, `depth ${ed.depth} capped by maxDepth`);
  assert.ok(ed.image.equals(expected), 'current image intact after evictions');

  // only the retained depth is undoable
  assert.equal(ed.undo(), true);
  assert.equal(ed.undo(), true);
  assert.equal(ed.undo(), true);
  assert.equal(ed.undo(), false, 'evicted history is gone, no corruption');
  const afterThree = new RgbaImage(10, 10);
  for (let i = 0; i < 3; i++) afterThree.setPixel(i, 0, [i * 30 + 1, 5, 9, 255]);
  assert.ok(ed.image.equals(afterThree), 'retained undos replay exactly');

  // editing still works on top of the evicted base
  ed.strokeRect(0, 5, 2, 2, G, 'post-eviction');
  assert.deepEqual(px(ed, 1, 5), G);
  ed.undo();
  assert.deepEqual(px(ed, 1, 5), T);
});

test('rle round-trip and in-memory compression keep undo correct', () => {
  const runHeavy = new Uint8Array(1000); // long zero runs
  runHeavy[500] = 7;
  const varied = Uint8Array.from({ length: 256 }, (_, i) => i);
  for (const src of [runHeavy, varied, new Uint8Array(0)]) {
    assert.deepEqual([...rleDecode(rleEncode(src))], [...src]);
  }
  assert.ok(rleEncode(runHeavy).length < runHeavy.length / 10, 'runs compress well');

  const ed = new Editor(new RgbaImage(8, 8), { compressAfter: 0 });
  ed.strokeRect(0, 0, 8, 8, R, 'fill'); // 64 px: raw delta would be 768 bytes
  ed.strokeRect(0, 0, 1, 1, B, 'dot');  // pushes 'fill' into compressed territory
  const stats = ed.memoryStats();
  assert.ok(stats.deltaBytes < 768, `compressed deltas use ${stats.deltaBytes} bytes`);

  ed.undo();
  ed.undo(); // exercises decompression of the compressed fill delta
  for (let y = 0; y < 8; y++)
    for (let x = 0; x < 8; x++)
      assert.deepEqual(px(ed, x, y), T);
});
