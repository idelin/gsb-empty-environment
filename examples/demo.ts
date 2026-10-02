/**
 * 运行样例：模拟连续导入 100 万条出租车载客点，
 * 然后回答“上周五晚高峰(18:00-20:00)全市人流密度最高的前 10 个网格中心经纬度”。
 *
 * 运行：node examples/demo.ts   （Node >= 22.6，原生 TypeScript 类型擦除）
 */

import { SpatioTemporalCube, HOUR_MS } from '../src/index.ts';

// 北京市区范围，200x200 瓦片网格（约 4 万网格），保留 7 天 = 168 个小时切片
const cube = new SpatioTemporalCube({
  minLng: 116.0, minLat: 39.6, maxLng: 116.8, maxLat: 40.2,
  cols: 200, rows: 200,
  retentionHours: 24 * 7,
});

// 上周五 00:00（UTC）作为模拟数据的时间基准
const friday = Date.UTC(2026, 9, 2, 0, 0, 0); // 2026-10-02 为周五

// 三个商圈热点（国贸、西二旗、望京），晚高峰权重更高
const hotspots = [
  { lng: 116.46, lat: 39.91, name: '国贸' },
  { lng: 116.30, lat: 40.05, name: '西二旗' },
  { lng: 116.47, lat: 39.99, name: '望京' },
];

let seed = 20261002;
const rand = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff;
// 以热点为中心的高斯扰动
const gauss = () => (rand() + rand() + rand() + rand() - 2) * 0.5;

const TOTAL = 1_000_000;
const t0 = performance.now();
for (let i = 0; i < TOTAL; i++) {
  const r = rand();
  let lng: number, lat: number;
  if (r < 0.55) {
    // 55% 的点聚集在热点周边
    const h = hotspots[Math.floor(rand() * hotspots.length)];
    lng = h.lng + gauss() * 0.02;
    lat = h.lat + gauss() * 0.02;
  } else {
    // 其余为全市均匀背景
    lng = 116.0 + rand() * 0.799;
    lat = 39.6 + rand() * 0.599;
  }
  // 时间分布：40% 落在晚高峰 18:00-20:00，其余散布全天
  const peak = rand() < 0.4;
  const hour = peak ? 18 + Math.floor(rand() * 2) : Math.floor(rand() * 24);
  const ts = friday + hour * HOUR_MS + Math.floor(rand() * HOUR_MS);
  cube.addPoint(lng, lat, ts, 1);
}
const ingestMs = performance.now() - t0;

// 查询：上周五晚高峰 18:00-20:00 Top-10
const winStart = friday + 18 * HOUR_MS;
const winEnd = friday + 20 * HOUR_MS;
const q0 = performance.now();
const top10 = cube.topK(winStart, winEnd, 10);
const queryMs = performance.now() - q0;

const stats = cube.stats();
const mem = process.memoryUsage();

console.log('=== 流式导入 ===');
console.log(`轨迹点总数:        ${TOTAL.toLocaleString()}`);
console.log(`成功落桶:          ${stats.acceptedPoints.toLocaleString()}`);
console.log(`丢弃(越界/过期):   ${stats.droppedPoints.toLocaleString()}`);
console.log(`导入耗时:          ${ingestMs.toFixed(0)} ms (${(TOTAL / ingestMs / 1000).toFixed(2)}M 点/秒)`);
console.log('');
console.log('=== 内存占用 ===');
console.log(`立方体内存预算:    ${(stats.memoryBudgetBytes / 1024 / 1024).toFixed(1)} MB (200x200 网格 x ${stats.retentionHours} 小时切片)`);
console.log(`进程 RSS:          ${(mem.rss / 1024 / 1024).toFixed(1)} MB`);
console.log(`堆使用:            ${(mem.heapUsed / 1024 / 1024).toFixed(1)} MB`);
console.log('');
console.log('=== 上周五晚高峰(18:00-20:00) Top-10 热点网格 ===');
console.log(`查询耗时:          ${queryMs.toFixed(1)} ms`);
console.log('排名  中心经度      中心纬度      密度权重');
for (const h of top10) {
  console.log(
    `#${String(h.rank).padEnd(3)}  ${h.lng.toFixed(4).padEnd(12)}  ${h.lat.toFixed(4).padEnd(12)}  ${h.weight}`,
  );
}
