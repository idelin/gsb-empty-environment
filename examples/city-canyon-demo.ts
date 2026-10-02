/**
 * 城市峡谷地图匹配演示：
 * 一段因多径效应剧烈漂移的 GPS 轨迹 → HMM/Viterbi 匹配 → 贴合道路中心线。
 * 运行：npm run demo
 */
import { buildCityCanyonScenario } from '../src/city-canyon.ts';
import { haversineM } from '../src/geometry.ts';
import { matchTrace } from '../src/map-matcher.ts';

const scenario = buildCityCanyonScenario(42);
const { network, gps, trueSegmentIds, truePositions } = scenario;

console.log('=== 城市峡谷 HMM 地图匹配演示 ===\n');
console.log(`路网：${network.segments.length} 条路段 / ${network.nodes.length} 个交叉口`);
console.log(`轨迹：${gps.length} 个 GPS 点（10 m/s，2s 采样）\n`);

const result = matchTrace(network, gps);

// 误差统计
const rawErrors = gps.map((p, i) => haversineM(p, truePositions[i]));
const matchedErrors = result.matched.map((m, i) => haversineM(m.matched, truePositions[i]));
const mean = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / xs.length;
const p95 = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length * 0.95)];

console.log(`原始 GPS 误差：mean=${mean(rawErrors).toFixed(1)}m  p95=${p95(rawErrors).toFixed(1)}m  max=${Math.max(...rawErrors).toFixed(1)}m`);
console.log(`匹配后  误差：mean=${mean(matchedErrors).toFixed(1)}m  p95=${p95(matchedErrors).toFixed(1)}m  max=${Math.max(...matchedErrors).toFixed(1)}m`);

const correct = result.matched.filter((m, i) => m.segmentId === trueSegmentIds[i]).length;
console.log(`路段识别正确率：${correct}/${gps.length} (${((100 * correct) / gps.length).toFixed(1)}%)`);
console.log(`最优路径对数概率：${result.logProb.toFixed(2)}\n`);

// 匹配到的路段序列（去重压缩）
const streetRun: string[] = [];
for (const m of result.matched) {
  if (streetRun[streetRun.length - 1] !== m.segmentName) streetRun.push(m.segmentName);
}
console.log(`匹配路径：${streetRun.join(' → ')}\n`);

// 漂移最严重的 8 个点的前后对比
console.log('漂移点纠正示例（原始误差 → 匹配后误差 → 吸附路段）：');
const worst = rawErrors
  .map((e, i) => ({ e, i }))
  .sort((a, b) => b.e - a.e)
  .slice(0, 8)
  .sort((a, b) => a.i - b.i);
for (const { i } of worst) {
  const m = result.matched[i];
  const truth = network.segment(trueSegmentIds[i]).name;
  const ok = m.segmentId === trueSegmentIds[i] ? '✓' : '✗';
  console.log(
    `  #${String(i).padStart(2)}  ${rawErrors[i].toFixed(1).padStart(5)}m → ${matchedErrors[i].toFixed(1).padStart(5)}m  ` +
      `吸附 ${m.segmentName}（真实 ${truth}）${ok}`,
  );
}
