import { performance } from 'node:perf_hooks';
import { DelaunayTriangulator, circumcircle, dist } from '../src/index.ts';
import type { Point } from '../src/index.ts';

/** Deterministic PRNG (mulberry32) so the sample is reproducible. */
function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const rng = mulberry32(20261004);
const N = 50;
const points: Point[] = Array.from({ length: N }, () => ({
  x: Math.round(rng() * 100000) / 100,
  y: Math.round(rng() * 100000) / 100,
}));

console.log(`=== Delaunay 三角剖分样例：${N} 个随机测绘点 ===\n`);

const t0 = performance.now();
const delaunay = new DelaunayTriangulator();
for (const p of points) delaunay.insert(p); // 逐点动态插入 + Lawson 翻边
const mesh = delaunay.toMesh();
const elapsed = performance.now() - t0;

const T = mesh.triangles.length;
const h = mesh.hull.length;
const E = (3 * T + h) / 2;

console.log(`点数 n            : ${N}`);
console.log(`三角形数 T        : ${T}   (Euler 公式 2n-2-h = ${2 * N - 2 - h})`);
console.log(`边数 E            : ${E}   (Euler 公式 3n-3-h = ${3 * N - 3 - h})`);
console.log(`凸包顶点数 h      : ${h}`);
console.log(`构建耗时          : ${elapsed.toFixed(2)} ms`);

console.log(`\n外边界凸包 (CCW): [${mesh.hull.join(', ')}]`);

console.log('\n前 5 个三角形的邻接拓扑:');
for (let t = 0; t < Math.min(5, T); t++) {
  const [a, b, c] = mesh.triangles[t];
  const [na, nb, nc] = mesh.neighbors[t];
  console.log(
    `  T${t}: 顶点 (${a}, ${b}, ${c})  邻居 [对边${a}->T${na}, 对边${b}->T${nb}, 对边${c}->T${nc}]`,
  );
}

// 验收：空外接圆性质 —— 任意三角形的外接圆内部不包含第四个点
let violations = 0;
let worst = -Infinity;
for (const [a, b, c] of mesh.triangles) {
  const { center, radius } = circumcircle(
    mesh.points[a],
    mesh.points[b],
    mesh.points[c],
  );
  for (let i = 0; i < N; i++) {
    if (i === a || i === b || i === c) continue;
    const gap = dist(center, mesh.points[i]) - radius;
    if (gap > worst) worst = gap;
    if (gap < -1e-6 * Math.max(radius, 1)) violations++;
  }
}

console.log('\n空外接圆性质验证:');
console.log(`  检查三角形数    : ${T}`);
console.log(`  违规数量        : ${violations}`);
console.log(`  最近点-圆间距   : ${worst.toFixed(4)} (>= 0 表示圆内无点)`);
console.log(
  `\n结果: ${violations === 0 && T === 2 * N - 2 - h ? 'PASS ✔ 三角网满足 Delaunay 空外接圆性质' : 'FAIL ✘'}`,
);
