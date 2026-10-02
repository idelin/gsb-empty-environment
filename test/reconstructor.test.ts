import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compress } from '../src/compressor.ts';
import { encodePacket, decodePacket } from '../src/encoder.ts';
import { restoreAt, resampleUniform, turnRadiusAt } from '../src/reconstructor.ts';
import { generateDriveCycle } from '../src/simulator.ts';
import { maxHeadingErrorDeg } from '../src/metrics.ts';

test('还原轨迹在保留点处航向角零失真', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  const decoded = decodePacket(encodePacket(kept));
  const restored = restoreAt(decoded, raw.map((p) => p.t));

  const keptTimes = new Set(kept.map((p) => p.t));
  const restoredAtKept = restored.filter((p) => keptTimes.has(p.t));
  assert.equal(restoredAtKept.length, kept.length);
  assert.equal(maxHeadingErrorDeg(decoded, restoredAtKept), 0);
});

test('还原轨迹在保留点处位置零失真', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  const restored = restoreAt(kept, kept.map((p) => p.t));
  for (let i = 0; i < kept.length; i++) {
    assert.equal(restored[i].lat, kept[i].lat);
    assert.equal(restored[i].lon, kept[i].lon);
    assert.equal(restored[i].ele, kept[i].ele);
    assert.equal(restored[i].heading, kept[i].heading);
  }
});

test('曲线转弯半径零失真（最急弯）', () => {
  const raw = generateDriveCycle();
  const kept = compress(raw);
  let minRadius = Infinity;
  let minIdx = -1;
  for (let i = 1; i < kept.length - 1; i++) {
    const r = turnRadiusAt(kept, i);
    if (r < minRadius) {
      minRadius = r;
      minIdx = i;
    }
  }
  assert.ok(minIdx > 0, 'expected a curved section');
  const rawIdx = raw.findIndex((p) => p.t === kept[minIdx].t);
  const rawRadius = turnRadiusAt(raw, rawIdx);
  assert.equal(minRadius, rawRadius, 'turn radius must be identical');
});

test('航向插值走最短弧（跨 0/360 边界）', () => {
  const kept = [
    { t: 0, lat: 31, lon: 121, ele: 0, speed: 10, heading: 350 },
    { t: 10_000, lat: 31.001, lon: 121, ele: 0, speed: 10, heading: 10 },
  ];
  const [mid] = restoreAt(kept, [5000]);
  assert.equal(mid.heading, 0);
});

test('均匀重采样覆盖全时段', () => {
  const kept = compress(generateDriveCycle());
  const resampled = resampleUniform(kept, 1000);
  assert.equal(resampled[0].t, kept[0].t);
  assert.ok(resampled[resampled.length - 1].t <= kept[kept.length - 1].t);
  assert.ok(resampled.length > kept.length);
});
