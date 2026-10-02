import { GestureRecognizer } from '../src/recognizer.ts';
import { buildFixtureTree } from '../src/fixtures.ts';
import { hitTest, measureText } from '../src/layout.ts';
import { GLYPHS } from '../src/fixtures.ts';
import type { GestureOutput, PointerInput, PointerEventType } from '../src/types.ts';

const P = (type: PointerEventType, pointerId: number, x: number, y: number, t: number, epoch = 0): PointerInput =>
  ({ type, pointerId, x, y, t, epoch });

const tree = buildFixtureTree();
const rec = new GestureRecognizer({ hitTest: (x, y) => hitTest(tree, x, y) });

function fmt(o: GestureOutput): string {
  switch (o.kind) {
    case 'preview':
      return `  <- preview ${o.gesture}  gen=${o.gen} dx=${o.dx.toFixed(1)} dy=${o.dy.toFixed(1)} scale=${o.scale.toFixed(3)}`;
    case 'compensate':
      return `  <- COMPENSATE ${o.gesture} gen=${o.gen} dx=${o.dx.toFixed(1)} dy=${o.dy.toFixed(1)} scale=${o.scale.toFixed(3)}`;
    case 'commit':
      return `  <- COMMIT ${o.gesture} gen=${o.gen} dx=${o.dx.toFixed(1)} dy=${o.dy.toFixed(1)} scale=${o.scale.toFixed(3)} dur=${o.duration}ms target=${o.targetId ?? '-'}`;
    case 'cancel':
      return `  <- cancel ${o.gesture} gen=${o.gen} (${o.reason})`;
    case 'ignored':
      return `  <- ignored ${o.inputType}#${o.pointerId} (${o.reason})`;
  }
}

function run(title: string, events: PointerInput[]): void {
  console.log(`\n== ${title} ${'='.repeat(Math.max(0, 60 - title.length))}`);
  for (const ev of events) {
    console.log(`-> ${ev.type}#${ev.pointerId} (${ev.x},${ev.y}) t=${ev.t} epoch=${ev.epoch ?? 0}`);
    for (const o of rec.handle(ev)) console.log(fmt(o));
  }
  console.log(`   phase=${rec.getPhase()} activePointers=${rec.activePointers()}`);
}

console.log(`fixture: "Pinch demo" @16px measures ${measureText('Pinch demo', 16, GLYPHS)}px wide (glyph fixture, no layout engine)`);

run('tap on the button -> click commit with hit target', [
  P('down', 1, 50, 180, 0),
  P('up', 1, 50, 180, 140),
]);

run('click turns into a drag past the 10px slop -> no click', [
  P('down', 1, 100, 350, 0),
  P('move', 1, 108, 352, 60),
  P('move', 1, 116, 356, 120),
  P('move', 1, 130, 360, 180),
  P('up', 1, 130, 360, 240),
]);

run('second finger joins mid-drag -> drag compensated, pinch commits', [
  P('down', 1, 100, 350, 0),
  P('move', 1, 124, 350, 60),
  P('down', 2, 220, 350, 120),
  P('move', 2, 250, 350, 180),
  P('move', 1, 104, 350, 240),
  P('up', 1, 104, 350, 300),
  P('up', 2, 250, 350, 360),
]);

run('pointer capture lost mid-drag -> compensate + cancel, never stuck', [
  P('down', 1, 100, 350, 0),
  P('move', 1, 140, 350, 60),
  P('lostcapture', 1, 140, 350, 120),
  P('move', 1, 160, 350, 180), // stray event for the lost pointer
]);

run('coordinate space changes mid-gesture -> re-baseline, no jump', [
  P('down', 1, 60, 350, 0),
  P('move', 1, 66, 350, 60),
  P('move', 1, 800, 700, 120, 1),
  P('move', 1, 815, 700, 180, 1),
  P('up', 1, 815, 700, 240, 1),
]);
