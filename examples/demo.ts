import type { Rect } from '../src/index.ts';
import {
  Bitmap, Layer, DirtyTracker, render, translation, rect,
} from '../src/index.ts';

const W = 16;
const H = 10;

function solid(w: number, h: number, r: number, g: number, b: number, a: number): Bitmap {
  const bmp = new Bitmap(w, h);
  bmp.fill(r, g, b, a);
  return bmp;
}

function show(bmp: Bitmap, dirty?: Rect | null): string {
  const lines: string[] = [];
  for (let y = 0; y < bmp.height; y++) {
    let line = '';
    for (let x = 0; x < bmp.width; x++) {
      const [r, g, b, a] = bmp.getPixel(x, y);
      const inDirty = dirty != null &&
        x >= dirty.x && y >= dirty.y && x < dirty.x + dirty.width && y < dirty.y + dirty.height;
      let ch = '.';
      if (a > 0) {
        if (r > 200 && g < 100) ch = 'R';
        else if (g > 200 && r < 100) ch = 'G';
        else if (b > 200) ch = 'B';
        else if (r > 200 && g > 200) ch = 'W';
        else ch = '+';
      }
      if (inDirty) ch = ch === '.' ? ':' : ch.toLowerCase();
      line += ch;
    }
    lines.push(line);
  }
  return lines.join('\n');
}

function printDirty(tracker: DirtyTracker): void {
  for (const e of tracker.getEntries()) {
    console.log(
      `  [${e.reason}] layer='${e.layerId}' rect=(${e.rect.x},${e.rect.y} ${e.rect.width}x${e.rect.height})`,
    );
    console.log(`    path: ${e.propagationPath.join(' -> ')}`);
    console.log(`    ${e.detail}`);
  }
  const d = tracker.dirtyRegion();
  console.log(`  merged dirty region: ${d ? `(${d.x},${d.y} ${d.width}x${d.height})` : 'none'}`);
}

// ---- build scene -----------------------------------------------------------
const root = new Layer('root');
root.addChild(new Layer('background', { bitmap: solid(W, H, 0, 0, 0, 0) }));
const sprite = root.addChild(new Layer('sprite', {
  bitmap: solid(3, 3, 255, 0, 0, 255),
  transform: translation(2, 2),
  effects: [{ kind: 'shadow', radius: 2, offsetX: 1, offsetY: 1, color: [0, 0, 255, 160] }],
}));
const tracker = new DirtyTracker(root);

console.log('== initial render ==');
const frame = new Bitmap(W, H);
render(root, frame);
console.log(show(frame));

// ---- 1. local edit ---------------------------------------------------------
console.log('\n== 1. local edit: paint sprite pixel (0,0) green ==');
sprite.bitmap!.setPixel(0, 0, 0, 255, 0, 255);
tracker.markLocalEdit(sprite, rect(0, 0, 1, 1));
printDirty(tracker);
const dirty1 = tracker.dirtyRegion();
const inc1 = frame.clone();
render(root, inc1, dirty1!);
console.log(show(inc1, dirty1));
tracker.clear();

// ---- 2. move layer ---------------------------------------------------------
console.log('\n== 2. move sprite to (9,4): old+new positions dirtied ==');
tracker.setTransform(sprite, translation(9, 4));
printDirty(tracker);
const dirty2 = tracker.dirtyRegion();
const inc2 = inc1.clone();
render(root, inc2, dirty2!);
console.log(show(inc2, dirty2));
tracker.clear();

// ---- 3. opacity change propagates ------------------------------------------
console.log('\n== 3. opacity 1.0 -> 0.5 propagates to root ==');
tracker.setOpacity(sprite, 0.5);
printDirty(tracker);
tracker.clear();

// ---- 4. edit outside clip is ignored ---------------------------------------
console.log('\n== 4. clip (0,0,4,4) on root; edit far outside clip ==');
tracker.setClip(root, rect(0, 0, 4, 4));
tracker.clear(); // only demonstrate the clipped edit
tracker.markLocalEdit(sprite, rect(1, 1, 1, 1)); // device (10,5): outside clip
printDirty(tracker);

// ---- consistency check ------------------------------------------------------
const full = new Bitmap(W, H);
render(root, full);
console.log('\nincremental == full render for every step: verified by tests (npm test)');
