import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  classifyPoint,
  classifyPointInRing,
  contains,
  isOnSegment,
  rayCastContains,
  windingNumber,
} from '../src/geofence.ts';
import {
  bowtieFence,
  pentagramFence,
  starFence,
  starTestPoints,
} from '../examples/star-fence.ts';

test('星型围栏：射线法与风向数法对所有验收点判定一致且符合拓扑定义', () => {
  for (const { label, point, expected } of starTestPoints) {
    assert.equal(
      classifyPoint(point, starFence, { rule: 'evenodd' }),
      expected,
      `[evenodd] ${label} (${point.lng},${point.lat})`,
    );
    assert.equal(
      classifyPoint(point, starFence, { rule: 'nonzero' }),
      expected,
      `[nonzero] ${label} (${point.lng},${point.lat})`,
    );
  }
});

test('星型围栏：实体部分判定为 inside', () => {
  const solid = starTestPoints.filter((p) => p.expected === 'inside');
  assert.ok(solid.length >= 5);
  for (const { point } of solid) {
    assert.equal(classifyPoint(point, starFence), 'inside');
    assert.equal(contains(point, starFence), true);
  }
});

test('星型围栏：孔洞（岛屿）内部判定为 outside，且不计入围栏', () => {
  const hollow = [
    { lng: 10, lat: 9.5 },
    { lng: 9, lat: 10 },
    { lng: 11, lat: 10.5 },
  ];
  for (const point of hollow) {
    assert.equal(classifyPoint(point, starFence), 'outside');
    assert.equal(contains(point, starFence), false);
    // 但相对外环本身它确实在内部 —— 证明是孔洞将其排除
    assert.equal(classifyPointInRing(point, starFence.outer), 'inside');
  }
});

test('星型围栏：顶点重合与边上临界点严格判定为 boundary', () => {
  const boundary = starTestPoints.filter((p) => p.expected === 'boundary');
  assert.ok(boundary.length >= 6);
  for (const { label, point } of boundary) {
    assert.equal(classifyPoint(point, starFence), 'boundary', label);
    // 边界默认计入围栏生效范围，可配置排除
    assert.equal(contains(point, starFence), true, label);
    assert.equal(contains(point, starFence, { includeBoundary: false }), false, label);
  }
});

test('边界严格定义：isOnSegment 含端点、排斥共线外延点', () => {
  const a = { lng: 10, lat: 0 };
  const b = { lng: 12, lat: 7 };
  assert.equal(isOnSegment({ lng: 11, lat: 3.5 }, a, b), true); // 中点
  assert.equal(isOnSegment({ lng: 10, lat: 0 }, a, b), true); // 端点（顶点重合）
  assert.equal(isOnSegment({ lng: 12, lat: 7 }, a, b), true); // 端点
  assert.equal(isOnSegment({ lng: 13, lat: 10.5 }, a, b), false); // 共线但在延长线上
  assert.equal(isOnSegment({ lng: 11, lat: 3.5000001 }, a, b), false); // 严格模式不容忍偏移
  assert.equal(
    isOnSegment({ lng: 11, lat: 3.5000001 }, a, b, 1e-4),
    true,
    '配置容差后容忍浮点噪声',
  );
});

test('射线恰好穿过顶点高度时不重复计数（半开区间约定）', () => {
  // (10,7) 的纬度与 4 个外环顶点相同，但不在任何边上
  const p = { lng: 10, lat: 7 };
  assert.equal(classifyPoint(p, starFence), 'inside');
  assert.equal(rayCastContains(p, starFence.outer), true);
  assert.notEqual(windingNumber(p, starFence.outer), 0);
});

test('凹多边形：星形凹陷区域（两臂之间）判定为 outside', () => {
  for (const point of [
    { lng: 3, lat: 14 },
    { lng: 17, lat: 14 },
    { lng: 2, lat: 3 },
    { lng: 18, lat: 3 },
  ]) {
    assert.equal(classifyPoint(point, starFence), 'outside');
  }
});

test('自相交五角星 {5/2}：evenodd 与 nonzero 规则给出各自正确定义的结果', () => {
  const center = { lng: 10, lat: 9 };
  // 中心被环绕两次：非零规则 -> inside，奇偶规则 -> outside
  assert.equal(windingNumber(center, pentagramFence.outer), 2);
  assert.equal(classifyPoint(center, pentagramFence, { rule: 'nonzero' }), 'inside');
  assert.equal(classifyPoint(center, pentagramFence, { rule: 'evenodd' }), 'outside');
  // 星尖区域只被覆盖一次：两种规则一致
  const tip = { lng: 10, lat: 1.5 };
  assert.equal(classifyPoint(tip, pentagramFence, { rule: 'evenodd' }), 'inside');
  assert.equal(classifyPoint(tip, pentagramFence, { rule: 'nonzero' }), 'inside');
});

test('自相交蝴蝶结：两瓣内部为 inside，交叉点为 boundary', () => {
  assert.equal(classifyPoint({ lng: 1, lat: 2 }, bowtieFence), 'inside');
  assert.equal(classifyPoint({ lng: 3, lat: 2 }, bowtieFence), 'inside');
  assert.equal(classifyPoint({ lng: 2, lat: 2 }, bowtieFence), 'boundary');
  assert.equal(classifyPoint({ lng: 2, lat: 0.5 }, bowtieFence), 'outside');
});

test('退化输入：重复闭合点与零长度边不影响判定', () => {
  const closedRing = {
    outer: [...starFence.outer, starFence.outer[0]], // 显式闭合，末边零长度
    holes: starFence.holes,
  };
  for (const { point, expected } of starTestPoints) {
    assert.equal(classifyPoint(point, closedRing), expected);
  }
});
