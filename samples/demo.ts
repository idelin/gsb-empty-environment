import { readFileSync } from 'node:fs';
import { KeyframeAnimation } from '../src/animation.ts';
import { Clock } from '../src/clock.ts';
import { Player } from '../src/player.ts';
import { Timeline } from '../src/timeline.ts';
import { applyProps, layout } from '../src/layout.ts';
import type { GlyphMetrics, LayoutNode } from '../src/layout.ts';

const fmt = (n: number) => Number(n.toFixed(4));
const show = (props: Record<string, number>) =>
  Object.entries(props)
    .map(([k, v]) => `${k}=${fmt(v)}`)
    .join(' ');
const section = (title: string) => console.log(`\n=== ${title} ===`);

// A manual time source keeps the whole demo deterministic.
let wall = 0;
const clock = new Clock(() => wall);

// --- 1. Keyframes + easing + repetition + direction -------------------------
section('1. keyframes + easing + iterations + direction (alternate)');
const bounce = new KeyframeAnimation({
  keyframes: [
    { offset: 0, easing: 'ease-in', props: { x: 0 } },
    { offset: 1, props: { x: 100 } },
  ],
  duration: 1,
  iterations: 2,
  direction: 'alternate',
});
for (const t of [0, 0.5, 1, 1.5, 2, 2.5]) {
  console.log(`  t=${t}  ${show(bounce.valueAt(t))}`);
}
console.log('  exact boundary t=2 lands on the end state, no extra round');

// --- 2. Pause/resume without drift ------------------------------------------
section('2. clock pause/resume accumulates no drift');
clock.start();
wall += 10;
clock.pause();
wall += 1e9; // wall time flies while paused
clock.resume();
wall += 5;
console.log(`  active time after 10 + (1e9 paused) + 5 = ${clock.now()} (expected 15)`);

// --- 3. Nested timeline: negative rate, pause segment, events ---------------
section('3. nested timeline: negative rate + pause segment + keyframe events');
const animA = new KeyframeAnimation({
  keyframes: [
    { offset: 0, props: { x: 0 } },
    { offset: 1, props: { x: 100 } },
  ],
  duration: 2,
  keyframeEvents: true,
});
const animB = new KeyframeAnimation({
  keyframes: [
    { offset: 0, props: { y: 0 } },
    { offset: 1, props: { y: 10 } },
  ],
  duration: 1,
  keyframeEvents: true,
});
const inner = new Timeline().add(animB, 0, -1); // plays end -> start
const root = new Timeline()
  .add(animA, 0, 1)
  .add(inner, 2, 1)
  .addPause(1, 0.5)
  .addMarker(1, 'mid');

const player = new Player(root, new Clock(() => 0));
console.log('  t     sequential        direct valueAt(t)');
for (const t of [0, 0.5, 1, 1.25, 2, 2.5, 3, 3.5, 4]) {
  player.advanceTo(t);
  const seq = show(player.value());
  const direct = show(root.valueAt(t));
  console.log(`  ${String(t).padEnd(5)} ${seq.padEnd(22)} ${direct}  ${seq === direct ? 'OK' : 'MISMATCH'}`);
}

// --- 4. Event delivery with cross-interval semantics ------------------------
section('4. events delivered for the crossed interval (t0, t1]');
const eventsPlayer = new Player(root, new Clock(() => 0));
eventsPlayer.advanceTo(3.5);
for (const e of eventsPlayer.events) {
  console.log(`  t=${e.time}  ${e.name}  direction=${e.direction}  at ${e.path}`);
}
const before = eventsPlayer.events.length;
eventsPlayer.advanceTo(3.5); // no movement: nothing re-fires
eventsPlayer.advanceTo(4); // interval (3.5, 4]: boundary event at 3.5 not re-sent
console.log(`  boundary events not re-fired: ${eventsPlayer.events.length === before ? 'OK' : 'FAIL'}`);

// --- 5. Time scale composes exactly -----------------------------------------
section('5. time scale composition uses exact time');
const scaled = new Player(root, clock);
scaled.timeScale = 0.1;
const t0 = clock.now();
wall += 30;
scaled.tick();
const exact = (t0 + 30) * 0.1;
console.log(`  position=${scaled.position}  exact=${exact}  equal=${scaled.position === exact ? 'OK' : 'FAIL'}`);

// --- 6. Reduced motion ------------------------------------------------------
section('6. reduced motion: deterministic constant output');
const reduced = new KeyframeAnimation({
  keyframes: [
    { offset: 0, easing: 'ease-in', props: { x: 0 } },
    { offset: 1, props: { x: 100 } },
  ],
  duration: 2,
  reducedMotion: true,
});
const outputs = [-5, 0, 0.7, 1.3, 999].map((t) => show(reduced.valueAt(t)));
console.log(`  outputs: ${outputs.join(' | ')}`);
console.log(`  deterministic: ${new Set(outputs).size === 1 ? 'OK' : 'FAIL'}`);

// --- 7. Hot-swap keyframes ---------------------------------------------------
section('7. hot-swap keyframes: old events invalidated, position resumes');
const swappable = new KeyframeAnimation({
  keyframes: [
    { offset: 0, props: { x: 0 } },
    { offset: 1, props: { x: 100 } },
  ],
  duration: 2,
  keyframeEvents: true,
});
swappable.addEvent(1, 'old-checkpoint');
const swapPlayer = new Player(swappable, new Clock(() => 0));
swapPlayer.advanceTo(1.5);
console.log(`  before swap: position=${swapPlayer.position} value=${show(swapPlayer.value())}`);
swappable.setKeyframes([
  { offset: 0, props: { x: 0 } },
  { offset: 0.5, props: { x: 500 } },
  { offset: 1, props: { x: 0 } },
]);
const delivered = swapPlayer.advanceTo(2);
console.log(`  after swap:  position=${swapPlayer.position} value=${show(swapPlayer.value())}`);
console.log(`  events after swap: ${delivered.map((e) => e.name).join(', ') || '(none)'}`);
console.log(`  old event invalidated: ${delivered.every((e) => e.name !== 'old-checkpoint') ? 'OK' : 'FAIL'}`);

// --- 8. Layout from fixtures, driven by animation ----------------------------
section('8. fixture-driven layout (no browser layout engine)');
const metrics = JSON.parse(
  readFileSync(new URL('../fixtures/glyph-metrics.json', import.meta.url), 'utf8'),
) as GlyphMetrics;
const nodeTree = JSON.parse(
  readFileSync(new URL('../fixtures/node-tree.json', import.meta.url), 'utf8'),
) as LayoutNode;
const widthAnim = new KeyframeAnimation({
  keyframes: [
    { offset: 0, props: { 'root.width': 120 } },
    { offset: 1, props: { 'root.width': 60 } },
  ],
  duration: 1,
});
for (const t of [0, 0.5, 1]) {
  const props = widthAnim.valueAt(t);
  const box = layout(applyProps(nodeTree, props), metrics);
  const title = box.children![0];
  console.log(
    `  t=${t}  root.width=${fmt(props['root.width'])}  root.height=${fmt(box.height)}  ` +
      `title lines=${title.lines!.length} [${title.lines!.map((l) => `"${l.text}"`).join(', ')}]`,
  );
}

console.log('\ndemo complete');
