import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SnapEngine, type SnapResult } from '../src/snap.ts';

/** Inclusive integer sweep, direction follows from/to. */
function sweep(from: number, to: number, step = 1): number[] {
  const out: number[] = [];
  const dir = Math.sign(step) * (from <= to ? 1 : -1);
  for (let v = from; dir > 0 ? v <= to : v >= to; v += Math.abs(step) * dir) {
    out.push(v);
  }
  return out;
}

function snapX(engine: SnapEngine, subpixel: number, y = 0): SnapResult {
  return engine.snap({ x: subpixel, y });
}

test('engage boundary: fresh sweep snaps exactly within snapThreshold', () => {
  // scale 2 subpx/px, vertical line at 100px (= 200 subpx), snap 5px (= 10 subpx).
  const line = { id: 1, orientation: 'vertical' as const, position: 100 };
  const snapped: number[] = [];
  for (const x of sweep(150, 250)) {
    // Fresh engine per sample: no hysteresis, isolates the engage boundary.
    const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
    engine.upsertLine(line);
    const r = snapX(engine, x);
    if (r.xSnap !== null) {
      snapped.push(x);
      assert.equal(r.x, 200, `snapped output must sit on the line (x=${x})`);
      assert.equal(r.xSnap.lineId, 1);
    } else {
      assert.equal(r.x, x, `free output must pass through (x=${x})`);
    }
  }
  assert.deepEqual(snapped, sweep(190, 210));
});

test('hysteresis: forward sweep holds to releaseThreshold, reverse re-engages at snapThreshold', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });

  // Start dead centre, walk away: engaged while |x-200|/2 <= 8, i.e. x <= 216.
  const forward = sweep(200, 250).map((x) => ({ x, r: snapX(engine, x) }));
  const forwardSnapped = forward.filter((s) => s.r.xSnap !== null).map((s) => s.x);
  assert.deepEqual(forwardSnapped, sweep(200, 216));
  assert.equal(engine.engagedLine('x'), null);

  // Walk back: nothing re-engages until |x-200|/2 <= 5, i.e. x <= 210;
  // from there hysteresis holds the snap down to |x-200|/2 <= 8 (x >= 184).
  const backward = sweep(250, 150).map((x) => ({ x, r: snapX(engine, x) }));
  const backwardSnapped = backward.filter((s) => s.r.xSnap !== null).map((s) => s.x);
  assert.deepEqual(backwardSnapped, sweep(210, 184));

  // The hysteresis band (211..216 subpx) is state-dependent, not toggling.
  const band = new Set(sweep(211, 216));
  for (const s of forward) {
    if (band.has(s.x)) assert.ok(s.r.xSnap !== null, `forward x=${s.x} should hold`);
  }
  for (const s of backward) {
    if (band.has(s.x)) assert.ok(s.r.xSnap === null, `backward x=${s.x} should stay free`);
  }
});

test('equal distance ties resolve to the smallest line id', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
  // Same position, different ids; inserted larger id first.
  engine.upsertLine({ id: 7, orientation: 'vertical', position: 100 });
  engine.upsertLine({ id: 3, orientation: 'vertical', position: 100 });
  assert.equal(snapX(engine, 200).xSnap?.lineId, 3);

  // Symmetric distances around the pointer.
  const engine2 = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine2.upsertLine({ id: 9, orientation: 'vertical', position: 98 });
  engine2.upsertLine({ id: 4, orientation: 'vertical', position: 102 });
  const r = snapX(engine2, 100);
  assert.equal(r.xSnap?.lineId, 4);
  assert.equal(r.xSnap?.distance, 2);
});

test('nearest line wins when distances differ', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  engine.upsertLine({ id: 2, orientation: 'vertical', position: 103 });
  assert.equal(snapX(engine, 102).xSnap?.lineId, 2);
});

test('deleting the engaged line releases the axis immediately', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  assert.ok(snapX(engine, 100).xSnap !== null);
  assert.equal(engine.engagedLine('x'), 1);

  engine.removeLine(1);
  assert.equal(engine.engagedLine('x'), null);
  const r = snapX(engine, 100);
  assert.equal(r.xSnap, null);
  assert.equal(r.x, 100);

  // Re-adding the line must not revive the old engagement: the pointer
  // sits in the hysteresis band (6 > snap 5, <= release 8) and stays free.
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  assert.equal(snapX(engine, 106).xSnap, null);
  // ...but entering the snap threshold engages normally.
  assert.ok(snapX(engine, 104).xSnap !== null);
});

test('moving the engaged line drags the snap along until the release gap', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  assert.ok(snapX(engine, 100).xSnap !== null);

  // Line walks away 1px per step; pointer stays at 100. The snapped
  // output tracks the line while the gap stays within releaseThreshold.
  for (const position of sweep(101, 108)) {
    engine.moveLine(1, position);
    const r = snapX(engine, 100);
    assert.equal(r.xSnap?.lineId, 1, `gap ${position - 100}px should hold`);
    assert.equal(r.x, position);
  }
  engine.moveLine(1, 109);
  const released = snapX(engine, 100);
  assert.equal(released.xSnap, null, 'gap 9px exceeds releaseThreshold 8');
  assert.equal(released.x, 100);
});

test('scale conversion: fractional line position against integer subpixels', () => {
  // scale 4 subpx/px, line at 50.25px = 201 subpx exactly, snap 2px = 8 subpx.
  const line = { id: 1, orientation: 'vertical' as const, position: 50.25 };
  const snapped: number[] = [];
  for (const x of sweep(185, 217)) {
    const engine = new SnapEngine({ snapThreshold: 2, releaseThreshold: 3, scale: 4 });
    engine.upsertLine(line);
    const r = snapX(engine, x);
    if (r.xSnap !== null) {
      snapped.push(x);
      assert.equal(r.x, 201);
    }
  }
  assert.deepEqual(snapped, sweep(193, 209));
});

test('setScale rescales distances for the same subpixel input', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  // 210 subpx = 105px at scale 2: distance 5, engages.
  assert.ok(snapX(engine, 210).xSnap !== null);
  // Quadrupling resolution: 210 subpx = 52.5px, far outside release.
  engine.setScale(4);
  const r = snapX(engine, 210);
  assert.equal(r.xSnap, null);
  assert.equal(r.x, 210);
  // 400 subpx = 100px at scale 4: dead on the line again.
  assert.equal(snapX(engine, 400).x, 400);
  assert.ok(engine.engagedLine('x') !== null);
});

test('horizontal and vertical snapping engage independently and simultaneously', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  engine.upsertLine({ id: 2, orientation: 'horizontal', position: 50 });

  const both = engine.snap({ x: 200, y: 100 });
  assert.equal(both.xSnap?.lineId, 1);
  assert.equal(both.ySnap?.lineId, 2);
  assert.deepEqual([both.x, both.y], [200, 100]);

  // Y walks out of release (220 subpx = 110px, gap 60px); X must be unaffected.
  const xOnly = engine.snap({ x: 202, y: 220 });
  assert.equal(xOnly.xSnap?.lineId, 1);
  assert.equal(xOnly.x, 200);
  assert.equal(xOnly.ySnap, null);
  assert.equal(xOnly.y, 220);

  // Orientation never crosses: a horizontal line cannot snap x.
  const engine2 = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine2.upsertLine({ id: 1, orientation: 'horizontal', position: 100 });
  const r = engine2.snap({ x: 100, y: 100 });
  assert.equal(r.xSnap, null);
  assert.ok(r.ySnap !== null);
});

test('jitter inside the hysteresis band never flickers', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  assert.ok(snapX(engine, 100).xSnap !== null);

  // Pointer oscillates noisily between 106 and 108 (beyond snap 5,
  // within release 8): output stays pinned to the line throughout.
  const trajectory = [106, 108, 107, 108, 106, 107, 108, 108, 106, 107];
  for (const x of trajectory) {
    const r = snapX(engine, x);
    assert.equal(r.xSnap?.lineId, 1, `x=${x} must stay engaged`);
    assert.equal(r.x, 100);
  }

  // A step past the release edge lets go, and the band is then free...
  assert.equal(snapX(engine, 109).xSnap, null);
  assert.equal(snapX(engine, 107).xSnap, null);
  // ...until the pointer re-enters the snap threshold.
  assert.ok(snapX(engine, 105).xSnap !== null);
});

test('engaged snap is sticky: a closer line does not steal it', () => {
  const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1 });
  engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
  assert.ok(snapX(engine, 100).xSnap !== null);
  // A new line appears closer to the pointer, but line 1 is still
  // within release distance, so the engagement holds.
  engine.upsertLine({ id: 2, orientation: 'vertical', position: 104 });
  const r = snapX(engine, 103);
  assert.equal(r.xSnap?.lineId, 1);
  assert.equal(r.x, 100);
});

test('constructor validates options', () => {
  assert.throws(() => new SnapEngine({ snapThreshold: 5, releaseThreshold: 4, scale: 1 }), RangeError);
  assert.throws(() => new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 0 }), RangeError);
  assert.throws(() => new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 1.5 }), RangeError);
});
