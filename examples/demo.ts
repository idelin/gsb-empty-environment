import { compressTrajectory } from '../src/compressor.ts';
import { decodeTrajectory } from '../src/decompressor.ts';
import { headingDeltaDeg, turningRadiusM } from '../src/geo.ts';
import { generateScenario } from './scenario.ts';

const raw = generateScenario({ hz: 10, noiseM: 1.5, headingNoiseDeg: 0.3 });
const packet = compressTrajectory(raw);
const restored = decodeTrajectory(packet);

const rawJsonBytes = Buffer.byteLength(JSON.stringify(raw.map(({ segment: _s, ...p }) => p)));
const rawBinBytes = raw.length * 48; // 6 × float64
const ratioJson = packet.length / rawJsonBytes;
const ratioBin = packet.length / rawBinBytes;

console.log('=== 时空轨迹压缩编码 · 运行样例 ===');
console.log(`原始点数:        ${raw.length}`);
console.log(`保留点数:        ${restored.length}  (采样率 ${(restored.length / raw.length * 100).toFixed(1)}%)`);
console.log(`原始体积(JSON):  ${rawJsonBytes} B`);
console.log(`原始体积(二进制): ${rawBinBytes} B`);
console.log(`压缩后体积:      ${packet.length} B`);
console.log(`压缩率(vs JSON):  ${(ratioJson * 100).toFixed(2)}%`);
console.log(`压缩率(vs 二进制): ${(ratioBin * 100).toFixed(2)}%`);
console.log(`验收线 ≤12%:     ${ratioJson <= 0.12 && ratioBin <= 0.12 ? '通过' : '未通过'}`);

// 分工况采样统计
const byT = new Map(raw.map((p) => [p.t, p]));
const segStat = new Map<string, { total: number; kept: number }>();
for (const p of raw) {
  const s = segStat.get(p.segment) ?? { total: 0, kept: 0 };
  s.total++;
  segStat.set(p.segment, s);
}
for (const p of restored) {
  const src = byT.get(p.t);
  if (src) segStat.get(src.segment)!.kept++;
}
console.log('\n--- 分工况采样 ---');
for (const [seg, s] of segStat) {
  console.log(`${seg.padEnd(16)} 原始 ${String(s.total).padStart(3)} 点 → 保留 ${String(s.kept).padStart(3)} 点`);
}

// 航向角 / 转弯半径零失真校验
let headingOk = true;
let radiusOk = true;
for (const p of restored) {
  const src = byT.get(p.t)!;
  if (p.heading !== src.heading) headingOk = false;
}
for (let i = 0; i + 1 < restored.length; i++) {
  const a = restored[i];
  const b = restored[i + 1];
  const sa = byT.get(a.t)!;
  const sb = byT.get(b.t)!;
  const yaw = headingDeltaDeg(a.heading, b.heading) / ((b.t - a.t) / 1000);
  const yawSrc = headingDeltaDeg(sa.heading, sb.heading) / ((sb.t - sa.t) / 1000);
  const r1 = turningRadiusM((a.speed + b.speed) / 2, yaw);
  const r2 = turningRadiusM((sa.speed + sb.speed) / 2, yawSrc);
  if (r1 !== r2) radiusOk = false;
}
console.log('\n--- 还原保真度 ---');
console.log(`航向角零失真:    ${headingOk ? '通过（逐位一致）' : '未通过'}`);
console.log(`转弯半径零失真:  ${radiusOk ? '通过（逐位一致）' : '未通过'}`);
