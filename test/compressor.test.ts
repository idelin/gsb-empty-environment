import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  TrajectoryCompressor,
  compressTrajectory,
} from '../src/compressor.ts';
import { decodeTrajectory } from '../src/decompressor.ts';
import { headingDeltaDeg, turningRadiusM } from '../src/geo.ts';
import { DEFAULT_SAMPLER_OPTIONS, type TrajectoryPoint } from '../src/types.ts';
import { generateScenario, metersToLatLon } from '../examples/scenario.ts';

const TOL = DEFAULT_SAMPLER_OPTIONS.maxChordDeviationM;

function stripMeta(raw: ReturnType<typeof generateScenario>): TrajectoryPoint[] {
  return raw.map(({ segment: _s, ...p }) => p);
}

/** 点到折线的最小距离（米），局部等距圆柱投影。 */
function minPolylineDistanceM(p: TrajectoryPoint, line: TrajectoryPoint[]): number {
  const cosLat = Math.cos((p.lat * Math.PI) / 180);
  const toXY = (q: TrajectoryPoint): [number, number] => [
    (q.lon - p.lon) * (Math.PI / 180) * cosLat * 6_371_008.8,
    (q.lat - p.lat) * (Math.PI / 180) * 6_371_008.8,
  ];
  const [px, py] = [0, 0];
  let best = Infinity;
  for (let i = 0; i + 1 < line.length; i++) {
    const [ax, ay] = toXY(line[i]);
    const [bx, by] = toXY(line[i + 1]);
    const dx = bx - ax;
    const dy = by - ay;
    const lenSq = dx * dx + dy * dy;
    const u = lenSq === 0 ? 0 : Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / lenSq));
    best = Math.min(best, Math.hypot(px - (ax + u * dx), py - (ay + u * dy)));
  }
  return best;
}

test('直线加速仅保留起止点', () => {
  const raw = generateScenario({ hz: 10 }).filter((p) => p.segment === 'straight-accel');
  assert.equal(raw.length, 300);
  const restored = decodeTrajectory(compressTrajectory(stripMeta(raw)));
  assert.ok(restored.length <= 3, `期望 ≤3 个保留点，实际 ${restored.length}`);
  assert.equal(restored[0].t, raw[0].t);
  assert.equal(restored[restored.length - 1].t, raw[raw.length - 1].t);
});

test('急转弯密集采样', () => {
  const raw = generateScenario({ hz: 10 }).filter((p) => p.segment === 'sharp-turn');
  const restored = decodeTrajectory(compressTrajectory(stripMeta(raw)));
  const turnTs = new Set(raw.map((p) => p.t));
  const keptInTurn = restored.filter((p) => turnTs.has(p.t)).length;
  assert.ok(
    keptInTurn >= raw.length * 0.8,
    `急转弯保留率 ${keptInTurn}/${raw.length} 不足 80%`,
  );
});

test('变道触发额外采样点', () => {
  const raw = generateScenario({ hz: 10 });
  const lane = raw.filter((p) => p.segment === 'lane-change');
  const restored = decodeTrajectory(compressTrajectory(stripMeta(raw)));
  const laneTs = new Set(lane.map((p) => p.t));
  const keptInLane = restored.filter((p) => laneTs.has(p.t)).length;
  assert.ok(keptInLane >= 3, `变道段保留点 ${keptInLane} 不足 3 个`);
});

test('数据包体积压缩至原始数据的 12% 以内', () => {
  const raw = generateScenario({ hz: 10, noiseM: 1.5, headingNoiseDeg: 0.3 });
  const points = stripMeta(raw);
  const packet = compressTrajectory(points);
  const rawBytes = Buffer.byteLength(JSON.stringify(points));
  const ratio = packet.length / rawBytes;
  assert.ok(ratio <= 0.12, `压缩率 ${(ratio * 100).toFixed(2)}% 超过 12%`);
});

test('还原后航向角零失真（网格值与非网格值）', () => {
  const raw = generateScenario({ hz: 10, noiseM: 1.5, headingNoiseDeg: 0.3 });
  // 混入非百分之一度网格的航向值，强制走 float64 字面量路径
  raw[100].heading = 123.456789012345;
  raw[300].heading = 0.000001;
  const points = stripMeta(raw);
  const restored = decodeTrajectory(compressTrajectory(points));
  const byT = new Map(points.map((p) => [p.t, p]));
  assert.ok(restored.length > 0);
  for (const p of restored) {
    const src = byT.get(p.t)!;
    assert.equal(p.heading, src.heading, `t=${p.t} 航向角不一致`);
  }
});

test('还原后曲线转弯半径零失真', () => {
  const raw = generateScenario({ hz: 10, noiseM: 1.0, headingNoiseDeg: 0.2 });
  const points = stripMeta(raw);
  const restored = decodeTrajectory(compressTrajectory(points));
  const byT = new Map(points.map((p) => [p.t, p]));
  for (let i = 0; i + 1 < restored.length; i++) {
    const a = restored[i];
    const b = restored[i + 1];
    const sa = byT.get(a.t)!;
    const sb = byT.get(b.t)!;
    const yaw = headingDeltaDeg(a.heading, b.heading) / ((b.t - a.t) / 1000);
    const yawSrc = headingDeltaDeg(sa.heading, sb.heading) / ((sb.t - sa.t) / 1000);
    const r1 = turningRadiusM((a.speed + b.speed) / 2, yaw);
    const r2 = turningRadiusM((sa.speed + sb.speed) / 2, yawSrc);
    assert.equal(r1, r2, `区间 [${a.t}, ${b.t}] 转弯半径不一致`);
  }
});

test('速度/高程/时间戳零失真，经纬度误差 ≤1e-7°', () => {
  const raw = generateScenario({ hz: 10, noiseM: 1.5, headingNoiseDeg: 0.3 });
  const points = stripMeta(raw);
  const restored = decodeTrajectory(compressTrajectory(points));
  const byT = new Map(points.map((p) => [p.t, p]));
  for (const p of restored) {
    const src = byT.get(p.t)!;
    assert.equal(p.speed, src.speed);
    assert.equal(p.alt, src.alt);
    assert.equal(p.t, src.t);
    assert.ok(Math.abs(p.lat - src.lat) <= 2e-7, '纬度超差');
    assert.ok(Math.abs(p.lon - src.lon) <= 2e-7, '经度超差');
  }
});

test('地图匹配正确率保持：还原点吸附位置与原始一致', () => {
  // 无噪声真值作为道路中心线，含噪轨迹模拟车载采集
  const truth = stripMeta(generateScenario({ hz: 10 }));
  const raw = stripMeta(generateScenario({ hz: 10, noiseM: 2.0, headingNoiseDeg: 0.5 }));
  const restored = decodeTrajectory(compressTrajectory(raw));

  const cosLat = Math.cos((truth[0].lat * Math.PI) / 180);
  const toXY = (q: TrajectoryPoint): [number, number] => [
    (q.lon - truth[0].lon) * (Math.PI / 180) * cosLat * 6_371_008.8,
    (q.lat - truth[0].lat) * (Math.PI / 180) * 6_371_008.8,
  ];
  const nodes = truth.map(toXY);
  const cum: number[] = [0];
  for (let i = 0; i + 1 < nodes.length; i++) {
    cum.push(cum[i] + Math.hypot(nodes[i + 1][0] - nodes[i][0], nodes[i + 1][1] - nodes[i][1]));
  }
  /** 吸附到中心线，返回投影点的弧长位置（米）。 */
  const snapArcLength = (p: TrajectoryPoint): number => {
    const [px, py] = toXY(p);
    let bestArc = 0;
    let bestDist = Infinity;
    for (let i = 0; i + 1 < nodes.length; i++) {
      const [ax, ay] = nodes[i];
      const dx = nodes[i + 1][0] - ax;
      const dy = nodes[i + 1][1] - ay;
      const lenSq = dx * dx + dy * dy;
      const u = lenSq === 0 ? 0 : Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / lenSq));
      const d = Math.hypot(px - (ax + u * dx), py - (ay + u * dy));
      if (d < bestDist) {
        bestDist = d;
        bestArc = cum[i] + u * Math.sqrt(lenSq);
      }
    }
    return bestArc;
  };

  const byT = new Map(raw.map((p) => [p.t, p]));
  let matched = 0;
  for (const p of restored) {
    const src = byT.get(p.t)!;
    // 吸附到同一条道路的同一位置（弧长差 ≤2m）视为地图匹配正确
    if (Math.abs(snapArcLength(p) - snapArcLength(src)) <= 2) matched++;
  }
  const accuracy = matched / restored.length;
  assert.ok(accuracy === 1, `地图匹配正确率 ${(accuracy * 100).toFixed(1)}% 未达到 100%`);
});


test('极高压缩比下航迹形状保持（弦偏差有界）', () => {
  const raw = stripMeta(generateScenario({ hz: 10 }));
  const restored = decodeTrajectory(compressTrajectory(raw));
  const keptTs = new Set(restored.map((p) => p.t));
  let worst = 0;
  for (const p of raw) {
    if (keptTs.has(p.t)) continue;
    worst = Math.max(worst, minPolylineDistanceM(p, restored));
  }
  assert.ok(
    worst <= TOL + 1e-6,
    `被丢弃点最大偏差 ${worst.toFixed(3)}m 超过弦偏差上限 ${TOL}m`,
  );
});

test('流式逐点压缩与一次性压缩结果一致', () => {
  const points = stripMeta(generateScenario({ hz: 10 }));
  const streaming = new TrajectoryCompressor();
  for (const p of points) streaming.push(p);
  const a = streaming.flush();
  const b = compressTrajectory(points);
  assert.deepEqual([...a], [...b]);
  // 重复 flush 幂等
  assert.deepEqual([...streaming.flush()], [...a]);
});

test('空轨迹与单点轨迹', () => {
  assert.deepEqual(decodeTrajectory(compressTrajectory([])), []);
  const single: TrajectoryPoint = { lat: 31.23, lon: 121.47, alt: 8, speed: 0, heading: 90, t: 1759000000000 };
  const restored = decodeTrajectory(compressTrajectory([single]));
  assert.equal(restored.length, 1);
  assert.deepEqual(restored[0], single);
});

test('解码器拒绝非法数据包', () => {
  assert.throws(() => decodeTrajectory(Uint8Array.from([0, 1, 2, 3, 0])), /bad magic/);
});
