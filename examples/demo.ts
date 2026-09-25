/**
 * Runnable tour of the engine. Every clock is fake and deterministic, so the
 * output is reproducible. Run with: node examples/demo.ts
 */
import { ticks, msOf } from '../src/time.ts';
import { easeInCubic } from '../src/easing.ts';
import { KeyframeTrack } from '../src/keyframes.ts';
import { Animation } from '../src/animation.ts';
import { Timeline } from '../src/timeline.ts';
import { Player } from '../src/player.ts';
import { layout } from '../src/layout.ts';
import { glyphFixture } from '../fixtures/glyphs.ts';
import { treeFixture } from '../fixtures/tree.ts';

const fmt = (v: number) => Math.round(v * 1000) / 1000;
const show = (obj: Record<string, number>) =>
  Object.entries(obj)
    .map(([k, v]) => `${k}=${fmt(v)}`)
    .join(' ');

// --- 1. Keyframes + easing + repeat + direction, random access ---------------
console.log('== 1. keyframes/easing/repeat/direction, random access ==');
const fade = new Animation(
  'fade',
  [
    new KeyframeTrack('opacity', [
      { at: 0, value: 0, easing: easeInCubic, event: 'begin' },
      { at: ticks(500), value: 0.5, event: 'half' },
      { at: ticks(1000), value: 1, event: 'end' },
    ]),
  ],
  { duration: ticks(1000), iterations: 2, direction: 'alternate', fill: 'both' },
);
const solo = new Player(fade, () => 0);
for (const ms of [0, 250, 500, 1000, 1250, 1999, 2000, 5000]) {
  console.log(`  t=${String(ms).padStart(4)}ms  ${show(solo.sampleAt(ticks(ms)))}`);
}

// --- 2. Nested timeline: negative rate + pause segment -----------------------
console.log('== 2. nested timeline: negative rate, pause segment ==');
const slide = new Animation(
  'slide',
  [
    new KeyframeTrack('x', [
      { at: 0, value: 0 },
      { at: ticks(1000), value: 100 },
    ]),
  ],
  { duration: ticks(1000), fill: 'both' },
);
const root = new Timeline('root');
root.add({ child: fade, offset: ticks(200) });
root.add({
  child: slide,
  offset: ticks(400),
  rate: -1,
  pauses: [{ start: ticks(800), duration: ticks(400) }],
});
root.addEvent('root-mark', ticks(600));
let now = 0;
const player = new Player(root, () => now);
for (const ms of [0, 400, 700, 900, 1200, 1500]) {
  console.log(`  t=${String(ms).padStart(4)}ms  ${show(player.sampleAt(ticks(ms)))}`);
}
console.log('  (slide freezes during the pause segment 800-1200ms and plays backwards)');

// --- 3. Events: crossing-interval delivery -----------------------------------
console.log('== 3. events delivered by crossing intervals ==');
player.seek(0);
for (const t of [300, 600, 700, 1200, 1700]) {
  now = t;
  const events = player
    .drainEvents()
    .map((e) => `${e.name}@${msOf(e.at)}ms(${e.path})`)
    .join(' ');
  console.log(`  drain at t=${String(t).padStart(4)}ms: ${events || '(none)'}`);
}

// --- 4. Pause/resume without drift -------------------------------------------
console.log('== 4. pause/resume accumulates zero drift ==');
now = 1000;
player.pause();
console.log(`  paused at ${msOf(player.currentTicks)}ms`);
now = 5000; // wall clock jumps while paused
console.log(`  still   at ${msOf(player.currentTicks)}ms after 4s of wall time`);
player.resume();
now = 5500;
console.log(`  resumed at ${msOf(player.currentTicks)}ms (exactly +500ms of play)`);

// --- 5. Hot-swap keyframes ----------------------------------------------------
console.log('== 5. hot-swap: old events invalid, position resumable ==');
const before = player.currentTicks;
fade.replaceTrack(
  new KeyframeTrack('opacity', [
    { at: 0, value: 1, event: 'swap-begin' },
    { at: ticks(1000), value: 0, event: 'swap-end' },
  ]),
);
console.log(`  version bumped to ${fade.version}; position kept at ${msOf(player.currentTicks)}ms (was ${msOf(before)}ms)`);
now = 5700;
console.log(`  events after swap: ${player.drainEvents().map((e) => `${e.name}@v${e.version}`).join(' ') || '(none)'}`);
player.seek(ticks(1000));
now = 6300;
console.log(`  re-seeked to 1000ms; new events: ${player.drainEvents().map((e) => `${e.name}@${msOf(e.at)}ms v${e.version}`).join(' ')}`);

// --- 6. Reduced motion --------------------------------------------------------
console.log('== 6. reduced motion: deterministic rest state ==');
player.reducedMotion = true;
for (const ms of [0, 1234, 99999]) {
  console.log(`  t=${String(ms).padStart(5)}ms  ${show(player.sampleAt(ticks(ms)))}`);
}

// --- 7. Fixture-driven layout -------------------------------------------------
console.log('== 7. layout from fixtures (no browser layout engine) ==');
const box = layout(treeFixture, 100, glyphFixture);
const printBox = (b: typeof box, depth: number) => {
  const pad = '  '.repeat(depth + 1);
  console.log(`${pad}${b.id ?? 'box'} x=${b.x} y=${b.y} w=${b.width} h=${b.height}`);
  for (const line of b.lines ?? []) {
    console.log(`${pad}  "${line.text}" (w=${line.width})`);
  }
  for (const child of b.children ?? []) printBox(child, depth + 1);
};
printBox(box, 0);
