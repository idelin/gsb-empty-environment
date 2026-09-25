import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { TileStore } from '../src/store.ts';
import { Document } from '../src/document.ts';
import { createImage, setPixel, getPixel, type Tile } from '../src/image.ts';

const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tilestore-demo-'));
console.log(`store dir: ${dir}`);

const store = TileStore.open(dir);
const doc = Document.create(store, 16, 16, 8);

function solidTile(size: number, r: number, g: number, b: number): Tile {
  const img = createImage(size, size, [r, g, b, 255]);
  return { width: size, height: size, pixels: img.pixels };
}

function gradientTile(size: number): Tile {
  const img = createImage(size, size);
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      setPixel(img, x, y, [x * 32, y * 32, 128, 255]);
    }
  }
  return { width: size, height: size, pixels: img.pixels };
}

function stats(label: string): void {
  const s = store.stats();
  console.log(
    `${label.padEnd(46)} blocks=${s.blocks} roots=${s.roots} leases=${s.leases} gen=${s.generation}`,
  );
}

doc.createLayer('background');
doc.createLayer('foreground');
stats('two blank layers (blank block shared)');

doc.editTile('background', 0, 0, gradientTile(8));
doc.editTile('background', 1, 1, solidTile(8, 30, 30, 30));
stats('background painted');

doc.snapshot('background', 'snap-v1');
stats('snapshot snap-v1 taken');

doc.editTile('background', 0, 0, solidTile(8, 0, 200, 200));
stats('background edited after snapshot (COW)');
console.log(`  snap-v1 pixel(0,0):  [${getPixel(doc.renderSnapshot('snap-v1'), 0, 0)}]`);
console.log(`  background pixel(0,0): [${getPixel(doc.renderLayer('background'), 0, 0)}]`);

doc.editTile('foreground', 1, 0, solidTile(8, 255, 128, 0));
stats('foreground edited independently');

store.acquireLease('reader-1', 'snap-v1');
doc.deleteSnapshot('snap-v1');
store.gc();
store.gc();
store.gc();
stats('snapshot deleted, 3 GC cycles, lease held');

store.releaseLease('reader-1');
store.gc();
store.gc();
stats('lease released, 2 more GC cycles');

console.log(`invariant problems: ${JSON.stringify(store.checkInvariant())}`);

const reopened = TileStore.open(dir);
const doc2 = Document.open(reopened);
console.log(`after reopen: roots=[${reopened.listRoots().sort().join(', ')}] blocks=${reopened.stats().blocks}`);
console.log(`reopened background pixel(0,0): [${getPixel(doc2.renderLayer('background'), 0, 0)}]`);
console.log(`reopened foreground pixel(8,0): [${getPixel(doc2.renderLayer('foreground'), 8, 0)}]`);
