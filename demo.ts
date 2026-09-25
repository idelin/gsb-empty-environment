import { Editor } from './src/editor.ts';
import { RgbaImage, type Color } from './src/rgba-image.ts';

const T: Color = [0, 0, 0, 0];
const R: Color = [255, 0, 0, 255];
const B: Color = [0, 0, 255, 255];
const G: Color = [0, 200, 0, 255];

function show(ed: Editor, title: string): void {
  const glyph = (c: Color) =>
    c[3] === 0 ? '.' : c[0] > 200 ? 'R' : c[2] > 200 ? 'B' : 'G';
  console.log(`\n== ${title} ==  (${ed.image.width}x${ed.image.height}, depth ${ed.depth}, at "${ed.label}")`);
  for (let y = 0; y < ed.image.height; y++) {
    let row = '';
    for (let x = 0; x < ed.image.width; x++) row += glyph(ed.image.getPixel(x, y));
    console.log('  ' + row);
  }
}

function stats(ed: Editor, title: string): void {
  const s = ed.memoryStats();
  console.log(`  [mem] ${title}: nodes=${s.nodes} deltaBytes=${s.deltaBytes} snapshotBytes=${s.snapshotBytes}`);
}

// 1. overlapping strokes, undo in reverse order
const ed = new Editor(new RgbaImage(12, 7), { compressAfter: 1 });
ed.strokeRect(1, 3, 9, 1, R, 'stroke-A (horizontal)');
ed.strokeRect(5, 1, 1, 5, B, 'stroke-B (vertical)');
show(ed, 'two overlapping strokes');
ed.undo();
show(ed, 'undo stroke-B: overlap (5,3) returns to red, A intact');
ed.redo();
show(ed, 'redo stroke-B');

// 2. crop is a metadata transaction; undo restores cropped pixels
ed.crop(2, 1, 8, 5, 'crop');
show(ed, 'crop to 8x5');
ed.undo();
show(ed, 'undo crop: full 12x7 canvas restored');

// 3. branching: undo then paint something else
ed.undo(); // undo stroke-B
ed.strokeRect(8, 1, 3, 3, G, 'stroke-C (green box)');
show(ed, 'branch: green box forks off stroke-A');
ed.undo();
console.log('  branches at stroke-A:', ed.branches().join(' | '));
ed.redo(0); // take the older branch explicitly
show(ed, 'redo(0): switch to the blue-stroke branch');

// 4. memory compression + eviction on a busy canvas
const busy = new Editor(new RgbaImage(16, 16), { maxDepth: 4, compressAfter: 1 });
for (let i = 0; i < 10; i++) {
  busy.strokeRect(i % 14, i % 14, 3, 1, i % 2 ? R : B, `dab-${i}`);
}
stats(busy, 'after 10 dabs (compressed + evicted, depth capped at 4)');
console.log(`  current depth=${busy.depth}; undo x4 still works, 5th returns false:`);
console.log('  undos:', [busy.undo(), busy.undo(), busy.undo(), busy.undo(), busy.undo()].join(' '));
show(busy, 'canvas after 4 undos — evicted history never corrupted pixels');
