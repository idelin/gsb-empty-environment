/** 运行样例：星型围栏 + 居中岛屿孔洞，逐点打印两内核与围栏判定结果。 */
import {
  locatePointInRing,
  locatePointInFence,
  evaluateFences,
  resolveDominantFence,
  type GeoFence,
  type LngLat,
} from '../src/geofence.ts';
import { buildStarFence } from '../src/presets.ts';

const CENTER: LngLat = [116.397, 39.908]; // 北京
const star = buildStarFence(CENTER, {
  id: 'star-fence',
  weight: 10,
  outerRadius: 0.05,
  innerRadius: 0.02,
  holeHalfSize: 0.008,
});

const overlay: GeoFence = {
  id: 'city-overlay',
  weight: 1,
  outer: [
    [116.367, 39.878],
    [116.427, 39.878],
    [116.427, 39.938],
    [116.367, 39.938],
  ],
};
const fences = [star, overlay];

const hole = star.holes![0];
const holeLat = hole[0][1]; // 孔洞底边纬度（精确浮点值）

const cases: Array<[string, LngLat]> = [
  ['星尖实体（正上方）', [116.397, 39.945]],
  ['星臂间凹口（54° 方向）', [116.397 + 0.03 * Math.cos((54 * Math.PI) / 180), 39.908 + 0.03 * Math.sin((54 * Math.PI) / 180)]],
  ['岛屿孔洞中心', [116.397, 39.908]],
  ['孔洞底边中点（边界）', [(hole[0][0] + hole[1][0]) / 2, holeLat]],
  ['孔洞顶点（边界）', hole[0]],
  ['星尖顶点（边界）', star.outer[0]],
  ['孔洞边上方 1e-9（属岛屿）', [116.397, holeLat + 1e-9]],
  ['孔洞边下方 1e-9（属实体）', [116.397, holeLat - 1e-9]],
  ['射线穿顶点临界点', [116.397, star.outer[9][1]]],
  ['围栏之外', [116.6, 40.1]],
];

console.log('星型围栏: 外半径 0.05°, 内半径 0.02°, 中心岛屿 0.008° 半边长, 中心 =', CENTER);
console.log('');
console.log(
  '场景'.padEnd(26, '　') +
  '射线法'.padEnd(10) +
  '风向数法'.padEnd(10) +
  '围栏判定'.padEnd(10) +
  '主导围栏(权重)',
);
for (const [label, p] of cases) {
  const eo = locatePointInRing(star.outer, p, { rule: 'even-odd' });
  const nz = locatePointInRing(star.outer, p, { rule: 'non-zero' });
  const fence = locatePointInFence(star, p);
  const dom = resolveDominantFence(fences, p);
  const domText = dom ? `${dom.fenceId} (w=${dom.weight}, ${dom.location})` : '-';
  console.log(
    label.padEnd(26, '　') +
    eo.padEnd(10) +
    nz.padEnd(10) +
    fence.padEnd(10) +
    domText,
  );
}

console.log('\n中心实体区全部命中（按权重降序）:');
for (const m of evaluateFences(fences, [116.397, 39.92])) {
  console.log(`  ${m.fenceId}: weight=${m.weight}, location=${m.location}`);
}
