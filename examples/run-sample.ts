import { compress } from '../src/compressor.ts';
import { encodePacket, decodePacket } from '../src/encoder.ts';
import { restoreAt, turnRadiusAt } from '../src/reconstructor.ts';
import {
  mapMatchAccuracy,
  maxCrossTrackErrorM,
  maxHeadingErrorDeg,
} from '../src/metrics.ts';
import { generateDriveCycle } from '../src/simulator.ts';

const raw = generateDriveCycle();

// 1. Stream the raw samples through the compressor (as the terminal would).
const kept = compress(raw);

// 2. Pack into the compact wire format and decode at the receiver.
const packet = encodePacket(kept);
const decoded = decodePacket(packet);

// 3. Restore a full-rate trajectory at the receiver.
const restored = restoreAt(decoded, raw.map((p) => p.t));

// --- metrics -------------------------------------------------------------
const rawBytes = Buffer.byteLength(JSON.stringify(raw));
const pointRatio = kept.length / raw.length;
const byteRatio = packet.length / rawBytes;

const keptTimes = new Set(kept.map((p) => p.t));
const restoredAtKept = restored.filter((p) => keptTimes.has(p.t));
const keptDecoded = decoded.filter((p) => keptTimes.has(p.t));

const headingErr = maxHeadingErrorDeg(keptDecoded, restoredAtKept);

// Turn-radius distortion at the sharpest kept curve point.
let minRadius = Infinity;
let minIdx = -1;
for (let i = 1; i < decoded.length - 1; i++) {
  const r = turnRadiusAt(decoded, i);
  if (r < minRadius) {
    minRadius = r;
    minIdx = i;
  }
}
const rawIdx = raw.findIndex((p) => p.t === decoded[minIdx].t);
const rawRadius = turnRadiusAt(raw, rawIdx);

const maxShapeErr = maxCrossTrackErrorM(raw, decoded);
const mmAccuracy = mapMatchAccuracy(raw, decoded, 15);

const pct = (x: number): string => `${(x * 100).toFixed(2)}%`;

console.log('=== 时空轨迹压缩编码器 · 运行样例 ===');
console.log(`原始采样点          : ${raw.length}`);
console.log(`保留采样点          : ${kept.length}  (${pct(pointRatio)} of points)`);
console.log(`原始数据包体积      : ${rawBytes} bytes (JSON)`);
console.log(`压缩数据包体积      : ${packet.length} bytes (TJC1 binary)`);
console.log(`体积压缩比          : ${pct(byteRatio)}  (目标 ≤ 12%)`);
console.log(`还原航向角最大误差  : ${headingErr.toExponential(3)} deg  (零失真)`);
console.log(`最急弯半径 原始/还原: ${rawRadius.toFixed(4)} m / ${minRadius.toFixed(4)} m`);
console.log(`转弯半径绝对误差    : ${Math.abs(rawRadius - minRadius).toExponential(3)} m  (零失真)`);
console.log(`航迹形状最大偏差    : ${maxShapeErr.toFixed(3)} m`);
console.log(`地图匹配正确率(15m) : ${pct(mmAccuracy)}`);

const reasons = new Map<string, number>();
for (const p of kept) {
  for (const r of p.reasons) reasons.set(r, (reasons.get(r) ?? 0) + 1);
}
console.log('保留点触发原因统计  :', Object.fromEntries(reasons));

if (byteRatio > 0.12) {
  console.error('FAIL: 体积压缩比超过 12%');
  process.exit(1);
}
if (headingErr !== 0 || rawRadius !== minRadius) {
  console.error('FAIL: 航向角/转弯半径存在失真');
  process.exit(1);
}
console.log('OK: 全部验收指标达标');
