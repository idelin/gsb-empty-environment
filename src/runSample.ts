// Runnable sample: drives the arbiter through three scripted scenarios and
// prints the resulting gesture-event stream. Run: npm run sample

import { readFileSync } from 'node:fs';
import { makeArbiter, type SceneFixture } from './harness.ts';
import type { PointerEventLike } from './types.ts';

const fixture = JSON.parse(readFileSync(new URL('../fixtures/scene.json', import.meta.url), 'utf8')) as SceneFixture;

function play(title: string, events: PointerEventLike[], ticks: number[] = []): void {
  console.log(`\n=== ${title} ===`);
  const arbiter = makeArbiter(fixture);
  const all: Array<{ t: number; run: () => void }> = [];
  for (const ev of events) {
    all.push({
      t: ev.time,
      run: () => {
        const r = arbiter.dispatch(ev);
        const note = r.dropped ? '  (dropped)' : '';
        console.log(`  <- ${ev.type}#${ev.pointerId} (${ev.x},${ev.y}) [${ev.space}]${note}`);
        for (const g of r.events) {
          console.log(`  -> ${g.kind}/${g.phase} gen=${g.generation} target=${g.targetId} ` +
            `dx=${g.dx.toFixed(1)} dy=${g.dy.toFixed(1)} scale=${g.scale.toFixed(2)}`);
        }
      },
    });
  }
  for (const t of ticks) {
    all.push({
      t,
      run: () => {
        console.log(`  <- tick(${t})`);
        for (const g of arbiter.tick(t)) console.log(`  -> ${g.kind}/${g.phase} gen=${g.generation}`);
      },
    });
  }
  all.sort((a, b) => a.t - b.t);
  for (const step of all) step.run();
  console.log(`  state=${arbiter.state}`);
}

let seq = 0;
const ev = (type: PointerEventLike['type'], pointerId: number, x: number, y: number, time: number, space = 'root'): PointerEventLike =>
  ({ seq: seq++, time, pointerId, type, x, y, space });

play('1. tap on the button -> click', [
  ev('pointerdown', 1, 55, 60, 0),
  ev('pointerup', 1, 55, 60, 90),
]);

play('2. press, move past threshold -> drag (no click)', [
  ev('pointerdown', 1, 50, 150, 0),
  ev('pointermove', 1, 62, 150, 40),
  ev('pointermove', 1, 90, 160, 80),
  ev('pointerup', 1, 90, 160, 120),
]);

play('3. drag, second finger joins -> drag compensated, pinch commits', [
  ev('pointerdown', 1, 100, 150, 0),
  ev('pointermove', 1, 120, 150, 40),   // drag preview
  ev('pointerdown', 2, 200, 150, 80),   // generation change
  ev('pointermove', 2, 250, 150, 120),  // pinch preview
  ev('pointerup', 2, 250, 150, 160),    // pinch commit
  ev('pointerup', 1, 120, 150, 200),
]);

play('4. capture lost mid-drag, then stale pointer expires', [
  ev('pointerdown', 1, 60, 150, 0),
  ev('pointermove', 1, 80, 150, 40),
  ev('lostpointercapture', 1, 80, 150, 80),
  ev('pointerdown', 2, 60, 150, 120),
  ev('pointermove', 2, 75, 150, 160),
], [6000]);
