import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  locatePointInRing,
  locatePointInFence,
  evaluateFences,
  resolveDominantFence,
  rayCastingInside,
  windingNumber,
  pointOnSegment,
  type GeoFence,
  type LngLat,
} from '../src/geofence.ts';
import { buildStarFence } from '../src/presets.ts';

// 北京天安门附近，星型围栏：外半径 0.05°，内半径 0.02°，中心镂空 0.008° 方形岛屿
const CENTER: LngLat = [116.397, 39.908];
const star = buildStarFence(CENTER, {
  id: 'star',
  weight: 10,
  outerRadius: 0.05,
  innerRadius: 0.02,
  holeHalfSize: 0.008,
});

test('星型凹多边形：星尖实体部分判定为 inside（两种内核一致）', () => {
  const p: LngLat = [116.397, 39.945]; // 正上方星尖
  assert.equal(locatePointInRing(star.outer, p, { rule: 'even-odd' }), 'inside');
  assert.equal(locatePointInRing(star.outer, p, { rule: 'non-zero' }), 'inside');
  assert.equal(locatePointInFence(star, p), 'inside');
});

test('星型凹多边形：凹口（notch）区域判定为 outside', () => {
  // 内凹顶点方向 54°，半径 0.03 > 内半径 0.02，位于星臂之间的凹陷区
  const angle = (54 * Math.PI) / 180;
  const p: LngLat = [116.397 + 0.03 * Math.cos(angle), 39.908 + 0.03 * Math.sin(angle)];
  assert.equal(locatePointInFence(star, p), 'outside');
  const far: LngLat = [116.6, 40.1];
  assert.equal(locatePointInFence(star, far), 'outside');
});

test('带孔洞：岛屿内部对围栏为 outside，对孔洞环为 inside', () => {
  const p: LngLat = [116.397, 39.908]; // 岛屿中心
  assert.equal(locatePointInRing(star.holes![0], p), 'inside');
  assert.equal(locatePointInFence(star, p), 'outside');
  const p2: LngLat = [116.401, 39.911]; // 岛屿内部偏移点
  assert.equal(locatePointInFence(star, p2), 'outside');
});

test('边界严格定义：孔洞边中点、孔洞顶点、外环顶点均为 boundary', () => {
  const hole = star.holes![0];
  const bottomMid: LngLat = [(hole[0][0] + hole[1][0]) / 2, hole[0][1]]; // 孔洞底边中点
  const leftMid: LngLat = [hole[0][0], (hole[0][1] + hole[3][1]) / 2]; // 孔洞左边中点
  assert.equal(locatePointInFence(star, bottomMid), 'boundary');
  assert.equal(locatePointInFence(star, hole[0]), 'boundary'); // 孔洞顶点重合
  assert.equal(locatePointInFence(star, leftMid), 'boundary');
  const tip = star.outer[0]; // 星尖顶点 (116.397, 39.958)
  assert.equal(locatePointInFence(star, tip), 'boundary'); // 外环顶点重合
});

test('边界临界点：eps=0 时 1e-9 级偏移即改变拓扑归属', () => {
  const holeLat = star.holes![0][0][1]; // 孔洞底边纬度（精确浮点值）
  const justInsideHole: LngLat = [116.397, holeLat + 1e-9]; // 孔洞底边上方 1e-9
  const justOutsideHole: LngLat = [116.397, holeLat - 1e-9]; // 孔洞底边下方 1e-9
  assert.equal(locatePointInFence(star, justInsideHole), 'outside'); // 属岛屿
  assert.equal(locatePointInFence(star, justOutsideHole), 'inside'); // 属实体
  // 放宽 eps 后同一点被判为边界
  assert.equal(locatePointInFence(star, justInsideHole, { eps: 1e-8 }), 'boundary');
});

test('射线恰好穿过顶点：半开区间规则保证不重不漏', () => {
  // 取内凹顶点（54° 与 126°）的精确纬度，射线 +x 方向恰好穿过该顶点
  const notchLat = star.outer[9][1];
  const p: LngLat = [116.397, notchLat];
  assert.equal(locatePointInRing(star.outer, p, { rule: 'even-odd' }), 'inside');
  assert.equal(locatePointInRing(star.outer, p, { rule: 'non-zero' }), 'inside');
});

test('简单凹多边形不变量：even-odd 与 non-zero 全网格一致', () => {
  for (let i = -40; i <= 40; i++) {
    for (let j = -40; j <= 40; j++) {
      const p: LngLat = [116.397 + i * 0.0015, 39.908 + j * 0.0015];
      const eo = locatePointInRing(star.outer, p, { rule: 'even-odd' });
      const nz = locatePointInRing(star.outer, p, { rule: 'non-zero' });
      assert.equal(eo, nz, `规则不一致 @ ${p}`);
    }
  }
});

test('自相交退化多边形（蝴蝶结）：两内核均支持且结果一致', () => {
  const bowtie = [[0, 0], [4, 4], [4, 0], [0, 4]] as const;
  // 左三角形内部
  assert.equal(locatePointInRing(bowtie, [1, 2], { rule: 'even-odd' }), 'inside');
  assert.equal(locatePointInRing(bowtie, [1, 2], { rule: 'non-zero' }), 'inside');
  // 两对角线之间（不属于任何三角形）
  assert.equal(locatePointInRing(bowtie, [2, 1], { rule: 'even-odd' }), 'outside');
  assert.equal(locatePointInRing(bowtie, [2, 1], { rule: 'non-zero' }), 'outside');
  // 对角线交点精确落在两条边上
  assert.equal(locatePointInRing(bowtie, [2, 2], { rule: 'even-odd' }), 'boundary');
  assert.equal(locatePointInRing(bowtie, [2, 2], { rule: 'non-zero' }), 'boundary');
});

test('自相交五角星 {5/2}：中心环绕数为 2，两规则给出定义的合法分歧', () => {
  const pentagram = [0, 1, 2, 3, 4].map((k) => {
    const angle = Math.PI / 2 + (k * 4 * Math.PI) / 5; // 每隔 144° 连点
    return [Math.cos(angle), Math.sin(angle)] as const;
  });
  const origin: LngLat = [0, 0];
  assert.equal(Math.abs(windingNumber(pentagram, origin)), 2);
  assert.equal(rayCastingInside(pentagram, origin), false); // even-odd: 穿越偶数次
  assert.equal(locatePointInRing(pentagram, origin, { rule: 'even-odd' }), 'outside');
  assert.equal(locatePointInRing(pentagram, origin, { rule: 'non-zero' }), 'inside');
});

test('带权重围栏集：命中按权重降序，主导围栏可取', () => {
  const overlay: GeoFence = {
    id: 'overlay',
    weight: 1,
    outer: [
      [116.367, 39.878],
      [116.427, 39.878],
      [116.427, 39.938],
      [116.367, 39.938],
    ],
  };
  const fences = [overlay, star]; // 故意乱序，验证排序
  // 星尖：仅命中高权围栏
  let dom = resolveDominantFence(fences, [116.397, 39.945]);
  assert.equal(dom?.fenceId, 'star');
  // 中心实体区：两者皆命中，高权优先
  const matches = evaluateFences(fences, [116.397, 39.92]);
  assert.deepEqual(matches.map((m) => m.fenceId), ['star', 'overlay']);
  assert.equal(matches[0].weight, 10);
  // 岛屿孔洞：高权围栏未命中，回落到低权围栏
  dom = resolveDominantFence(fences, [116.397, 39.908]);
  assert.equal(dom?.fenceId, 'overlay');
  // 完全外部：无命中
  assert.equal(resolveDominantFence(fences, [116.6, 40.1]), null);
});

test('pointOnSegment：顶点重合与端点语义', () => {
  assert.equal(pointOnSegment([1, 1], [0, 0], [2, 2]), true);
  assert.equal(pointOnSegment([0, 0], [0, 0], [2, 2]), true); // 端点
  assert.equal(pointOnSegment([2, 2], [0, 0], [2, 2]), true); // 端点
  assert.equal(pointOnSegment([3, 3], [0, 0], [2, 2]), false); // 共线但越界
  assert.equal(pointOnSegment([1, 1 + 1e-12], [0, 0], [2, 2]), false); // 严格 eps=0
});
