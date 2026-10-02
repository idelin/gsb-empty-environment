import { classifyPoint, contains } from '../src/geofence.ts';
import { starFence, starTestPoints } from './star-fence.ts';

console.log('=== 星型地理围栏（含岛屿镂空）点判定演示 ===\n');
console.log('外环顶点数: 10（五角星凹多边形）, 孔洞: 1（中央方形岛屿）\n');

const rows = starTestPoints.map(({ label, point, expected }) => {
  const evenodd = classifyPoint(point, starFence, { rule: 'evenodd' });
  const nonzero = classifyPoint(point, starFence, { rule: 'nonzero' });
  const fenced = contains(point, starFence);
  const ok = evenodd === expected && nonzero === expected ? 'OK' : 'MISMATCH';
  return { label, point: `(${point.lng}, ${point.lat})`, expected, evenodd, nonzero, fenced, ok };
});

console.table(rows);

const mismatches = rows.filter((r) => r.ok !== 'OK');
console.log(mismatches.length === 0 ? '全部测试点判定符合拓扑定义。' : `发现 ${mismatches.length} 处不一致！`);
