import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { SpacetimeGridIndex } from '../src/grid-index.ts';
import { computeContactIntervals, proveIntersection } from '../src/prover.ts';
import { verifyProof, trajectoryDigest } from '../src/certificate.ts';
import { CASE_A, CASE_B, MALL_ZONES, BASE } from '../src/scenario.ts';
import type { Trajectory } from '../src/types.ts';

const MIN = 60_000;

test('精确求交：静止点与匀速直线运动的相交区间解析解', () => {
  // 甲静止于原点；乙以 1 m/s 沿 x 轴从 (-100, 0) 走到 (100, 0)，阈值 10m
  const a: Trajectory = {
    entityId: 'A',
    points: [
      { t: 0, x: 0, y: 0 },
      { t: 200_000, x: 0, y: 0 },
    ],
  };
  const b: Trajectory = {
    entityId: 'B',
    points: [
      { t: 0, x: -100, y: 0 },
      { t: 200_000, x: 100, y: 0 },
    ],
  };
  const intervals = computeContactIntervals(a, b, 10);
  assert.equal(intervals.length, 1);
  // 解析解：|x(t)| = |t/1000 - 100| <= 10 -> t ∈ [90s, 110s]
  assert.ok(Math.abs(intervals[0].start - 90_000) < 1e-6, `start=${intervals[0].start}`);
  assert.ok(Math.abs(intervals[0].end - 110_000) < 1e-6, `end=${intervals[0].end}`);
  assert.ok(Math.abs(intervals[0].minDistance) < 1e-9);
  assert.ok(Math.abs(intervals[0].minDistanceAt - 100_000) < 1e-6);
});

test('精确求交：永不接近则无相交；擦边阈值边界正确', () => {
  const a: Trajectory = {
    entityId: 'A',
    points: [
      { t: 0, x: 0, y: 0 },
      { t: 100_000, x: 0, y: 0 },
    ],
  };
  const far: Trajectory = {
    entityId: 'B',
    points: [
      { t: 0, x: 0, y: 50 },
      { t: 100_000, x: 0, y: 50 },
    ],
  };
  assert.equal(computeContactIntervals(a, far, 10).length, 0);
  // 最近距离恰好等于阈值（y=10）时应判定为相交
  const touch: Trajectory = {
    entityId: 'C',
    points: [
      { t: 0, x: -50, y: 10 },
      { t: 100_000, x: 50, y: 10 },
    ],
  };
  const hit = computeContactIntervals(a, touch, 10);
  assert.equal(hit.length, 1);
  assert.ok(Math.abs(hit[0].minDistance - 10) < 1e-9);
});

test('楼层隔离：水平重合但不同楼层（层高4m）在10m阈值下的判定', () => {
  const a: Trajectory = {
    entityId: 'A',
    points: [
      { t: 0, x: 0, y: 0, floor: 1 },
      { t: 100_000, x: 0, y: 0, floor: 1 },
    ],
  };
  const b: Trajectory = {
    entityId: 'B',
    points: [
      { t: 0, x: 0, y: 0, floor: 2 },
      { t: 100_000, x: 0, y: 0, floor: 2 },
    ],
  };
  // 垂直距离 4m < 10m，应判定接触（同空间相邻楼层仍属密切接触场景）
  assert.equal(computeContactIntervals(a, b, 10, 4).length, 1);
  // 层高设为 15m 时则不接触
  assert.equal(computeContactIntervals(a, b, 10, 15).length, 0);
});

test('时间切片倒排网格索引：候选范围覆盖真实相交且不误报漏报', () => {
  const index = new SpacetimeGridIndex(10, 60_000);
  index.addTrajectory(CASE_A);
  index.addTrajectory(CASE_B);
  const ranges = index.candidateRanges(CASE_A.entityId, CASE_B.entityId, 10);
  assert.ok(ranges.length >= 3, `候选范围数=${ranges.length}`);
  // 候选范围必须覆盖全部真实相交时刻（三个柜台时段）
  const contactCenters = [
    BASE + 19 * MIN,   // 咖啡柜台
    BASE + 69 * MIN,   // 生鲜柜台
    BASE + 149 * MIN,  // 收银台A
  ];
  for (const c of contactCenters) {
    assert.ok(
      ranges.some((r) => c >= r.start && c <= r.end),
      `候选范围未覆盖 ${new Date(c).toISOString()}`,
    );
  }
});

test('疾控验收场景：检出3个<10m相交时段，地点证书指向正确柜台', () => {
  const index = new SpacetimeGridIndex(10, 60_000);
  index.addTrajectory(CASE_A);
  index.addTrajectory(CASE_B);
  const proof = proveIntersection(index, CASE_A.entityId, CASE_B.entityId, 10, {
    zones: MALL_ZONES,
    generatedAt: BASE,
  });

  assert.equal(proof.certificates.length, 3, `实际检出 ${proof.certificates.length} 段`);
  const zones = proof.certificates.map((c) => c.zone);
  assert.deepEqual(zones, ['咖啡柜台', '生鲜柜台', '收银台A']);

  for (const cert of proof.certificates) {
    assert.ok(cert.minDistance < 10, `最近距离 ${cert.minDistance}`);
    assert.ok(cert.end > cert.start);
    assert.match(cert.hash, /^[0-9a-f]{64}$/);
    assert.ok(cert.location.floor === 1);
  }
  // 时段单调递增且互不重叠
  for (let i = 1; i < proof.certificates.length; i++) {
    assert.ok(proof.certificates[i].start > proof.certificates[i - 1].end);
  }
});

test('不可抵赖性：证明链验证通过，任何篡改均被检出', () => {
  const index = new SpacetimeGridIndex(10, 60_000);
  index.addTrajectory(CASE_A);
  index.addTrajectory(CASE_B);
  const proof = proveIntersection(index, CASE_A.entityId, CASE_B.entityId, 10, {
    zones: MALL_ZONES,
    generatedAt: BASE,
  });

  assert.equal(verifyProof(proof, [CASE_A, CASE_B]).ok, true);

  // 篡改地点
  const t1 = structuredClone(proof);
  t1.certificates[0].zone = '服装柜台';
  assert.equal(verifyProof(t1, [CASE_A, CASE_B]).ok, false);

  // 篡改时间
  const t2 = structuredClone(proof);
  t2.certificates[1].start += 60_000;
  assert.equal(verifyProof(t2, [CASE_A, CASE_B]).ok, false);

  // 删除中间证书（断链）
  const t3 = structuredClone(proof);
  t3.certificates.splice(1, 1);
  assert.equal(verifyProof(t3, [CASE_A, CASE_B]).ok, false);

  // 替换输入轨迹（与承诺不符）
  const fakeB = structuredClone(CASE_B);
  fakeB.points[0].x += 1;
  assert.equal(verifyProof(proof, [CASE_A, fakeB]).ok, false);

  // 轨迹承诺确定性
  assert.equal(trajectoryDigest(CASE_A), trajectoryDigest(structuredClone(CASE_A)));
});

test('性能：24小时1Hz轨迹（86400采样点/人）求交在毫秒级完成', () => {
  const day = 24 * 3600 * 1000;
  const mk = (id: string, seed: number, plantContacts: boolean): Trajectory => {
    const points = [];
    let x = seed * 1000;
    let y = seed * 500;
    for (let t = 0; t <= day; t += 1000) {
      // 确定性伪随机游走
      const h = Math.sin(t * 0.001 + seed) * 43758.5453;
      const r = h - Math.floor(h);
      x += (r - 0.5) * 4;
      y += (Math.sin(t * 0.0007 + seed * 2) - 0.5) * 0.4;
      points.push({ t, x, y, floor: 1 });
    }
    if (plantContacts) {
      // 在第 12 小时附近强制贴近实体0的位置（接触 5 分钟）
      for (let t = 12 * 3600 * 1000; t <= 12 * 3600 * 1000 + 300_000; t += 1000) {
        const idx = t / 1000;
        points[idx] = { t, x: 0, y: 2, floor: 1 };
      }
    }
    return { entityId: id, points };
  };
  const a = mk('P0', 1, false);
  // 让 P0 在 12h 附近位于原点附近
  for (let t = 11 * 3600 * 1000; t <= 13 * 3600 * 1000; t += 1000) {
    a.points[t / 1000] = { t, x: 0, y: 0, floor: 1 };
  }
  const b = mk('P1', 7, true);

  const index = new SpacetimeGridIndex(10, 60_000);
  index.addTrajectory(a);
  index.addTrajectory(b);

  const t0 = performance.now();
  const proof = proveIntersection(index, 'P0', 'P1', 10, { generatedAt: 0 });
  const elapsed = performance.now() - t0;

  console.log(`    [性能] 86400采样点/人, 求交耗时 ${elapsed.toFixed(2)} ms, 检出 ${proof.certificates.length} 段`);
  assert.ok(proof.certificates.length >= 1);
  const contact = proof.certificates.find((c) => c.start <= 12 * 3600 * 1000 + 150_000 && c.end >= 12 * 3600 * 1000 + 150_000);
  assert.ok(contact, '未检出植入的接触时段');
  assert.ok(elapsed < 1000, `耗时 ${elapsed.toFixed(1)}ms 超出毫秒级要求`);
});
