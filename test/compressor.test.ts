import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TrajectoryCompressor, compress } from '../src/compressor.ts';
import { generateDriveCycle } from '../src/simulator.ts';
import { maxCrossTrackErrorM, mapMatchAccuracy } from '../src/metrics.ts';
import type { TrajectoryPoint } from '../src/types.ts';

function straightLine(count: number, speedMs: number): TrajectoryPoint[] {
  const pts: TrajectoryPoint[] = [];
  let lat = 31.0;
  for (let i = 0; i < count; i++) {
    lat += speedMs / 111_320;
    pts.push({ t: i * 1000, lat, lon: 121.0, ele: 10, speed: speedMs, heading: 0 });
  }
  return pts;
}

test('直线加速仅保留起止点（maxGap 内）', () => {
  // 25 s of straight-line acceleration 0 -> 25 m/s, well under maxGapM.
  const pts: TrajectoryPoint[] = [];
  let lat = 31.0;
  let speed = 0;
  for (let i = 0; i < 25; i++) {
    speed += 1;
    lat += speed / 111_320;
    pts.push({ t: i * 1000, lat, lon: 121.0, ele: 10, speed, heading: 90 });
  }
  const kept = compress(pts);
  assert.equal(kept.length, 2);
  assert.deepEqual(kept[0].t, pts[0].t);
  assert.deepEqual(kept[1].t, pts[pts.length - 1].t);
  assert.deepEqual(kept[0].reasons, ['boundary']);
  assert.deepEqual(kept[1].reasons, ['boundary']);
});

test('急转弯触发密集采样', () => {
  const pts: TrajectoryPoint[] = [];
  let lat = 31.0;
  let heading = 0;
  // 10 s straight, then 10 s of 15 deg/s yaw (sharp turn), then straight.
  for (let i = 0; i < 30; i++) {
    if (i >= 10 && i < 20) heading += 15;
    lat += (10 * Math.cos((heading * Math.PI) / 180)) / 111_320;
    pts.push({ t: i * 1000, lat, lon: 121.0, ele: 10, speed: 10, heading });
  }
  const kept = compress(pts);
  const inTurn = kept.filter((p) => p.t >= 10_000 && p.t <= 19_000);
  // Every sample inside the sharp turn must be retained.
  assert.equal(inTurn.length, 10);
  assert.ok(inTurn.every((p) => p.reasons.includes('turn-rate')));
});

test('高程落差触发保留', () => {
  const pts: TrajectoryPoint[] = [];
  let lat = 31.0;
  for (let i = 0; i < 40; i++) {
    lat += 15 / 111_320;
    const ele = i >= 20 ? 10 + (i - 19) * 2 : 10; // 2 m/s climb after t=20
    pts.push({ t: i * 1000, lat, lon: 121.0, ele, speed: 15, heading: 0 });
  }
  const kept = compress(pts);
  assert.ok(kept.some((p) => p.reasons.includes('elevation')));
});

test('长直线受 maxGap 约束稀疏保留', () => {
  const pts = straightLine(200, 20); // 200 s * 20 m/s = 4000 m
  const kept = compress(pts, { maxGapM: 400 });
  // ~4000/400 ≈ 10 gap points + 2 boundary.
  assert.ok(kept.length <= 13, `expected <= 13 kept, got ${kept.length}`);
  assert.ok(kept.some((p) => p.reasons.includes('max-gap')));
});

test('流式与批量结果一致', () => {
  const raw = generateDriveCycle();
  const batch = compress(raw);
  const stream = new TrajectoryCompressor();
  const streamed = [];
  for (const p of raw) {
    streamed.push(...stream.push(p));
  }
  const tail = stream.flush();
  if (tail) streamed.push(tail);
  assert.deepEqual(streamed, batch);
});

test('保留点为原始样本的零失真拷贝', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  const byTime = new Map(raw.map((p) => [p.t, p]));
  for (const k of kept) {
    const src = byTime.get(k.t);
    assert.ok(src, 'kept point must come from raw stream');
    for (const key of ['lat', 'lon', 'ele', 'speed', 'heading'] as const) {
      assert.equal(k[key], src[key], `${key} must be bit-exact`);
    }
  }
});

test('端到端：压缩比与形状保真', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  const ratio = kept.length / raw.length;
  assert.ok(ratio <= 0.12, `point ratio ${ratio} exceeds 12%`);
  const maxErr = maxCrossTrackErrorM(raw, kept);
  assert.ok(maxErr < 15, `shape error ${maxErr} m too large`);
  assert.equal(mapMatchAccuracy(raw, kept, 15), 1);
});
