import { HASHES } from './hashes.ts';
import {
  analyzeLoad,
  varianceCurve,
  bitWidthCurve,
  avalancheReport,
  randomKeyGen,
  sequentialKeyGen,
  verdictOf,
  type LoadReport,
} from './analyzer.ts';

function argNum(name: string, fallback: number): number {
  const prefix = `--${name}=`;
  for (const a of process.argv.slice(2)) {
    if (a.startsWith(prefix)) return Number(a.slice(prefix.length));
  }
  return fallback;
}

const SAMPLES = argNum('samples', 3_000_000);
const NODE_COUNT = argNum('nodes', 8);
const VNODES = argNum('vnodes', 160);
const SEED = argNum('seed', 0xc0ffee);

const NODES = Array.from({ length: NODE_COUNT }, (_, i) => `node-${i}`);

function pad(s: string | number, w: number): string {
  return String(s).padStart(w);
}

function fmtP(p: number): string {
  if (p === 0) return '<1e-300';
  if (p < 0.0001) return p.toExponential(2);
  return p.toFixed(4);
}

function printLoadTable(title: string, reports: LoadReport[]): void {
  console.log(`\n## ${title}`);
  console.log(
    [
      pad('哈希', 12),
      pad('样本数', 10),
      pad('标准差', 11),
      pad('CV实测', 9),
      pad('CV结构', 9),
      pad('CV比值', 8),
      pad('Max/Min', 9),
      pad('卡方χ²*', 10),
      pad('p值', 10),
      pad('判定', 6),
    ].join(''),
  );
  console.log('-'.repeat(100));
  for (const r of reports) {
    console.log(
      [
        pad(r.hashName, 12),
        pad(r.samples.toLocaleString('en-US'), 10),
        pad(r.stddev.toFixed(2), 11),
        pad((r.cv * 100).toFixed(3) + '%', 9),
        pad((r.structuralCv * 100).toFixed(3) + '%', 9),
        pad(r.cvRatio.toFixed(2), 8),
        pad(r.maxMinRatio.toFixed(3), 9),
        pad(r.chi2.toFixed(2), 10),
        pad(fmtP(r.pValue), 10),
        pad(r.verdict, 6),
      ].join(''),
    );
  }
  console.log('注：χ²* 为弧长加权卡方（剔除环结构不平衡，只检验键映射均匀性）；CV比值理想值≈1。');
}

console.log('='.repeat(104));
console.log('一致性哈希质量诊断报告');
console.log(
  `节点数=${NODE_COUNT}  虚拟节点/节点=${VNODES}  主样本量=${SAMPLES.toLocaleString('en-US')}  种子=${SEED}`,
);
console.log('='.repeat(104));

// ── 1. 负载均匀度：随机键 ─────────────────────────────────────────────
{
  const reports = HASHES.map((h) =>
    analyzeLoad({
      hashFn: h.fn,
      hashName: h.name,
      nodes: NODES,
      samples: SAMPLES,
      keyGen: randomKeyGen(SEED),
      keyType: 'random',
      vnodes: VNODES,
    }),
  );
  printLoadTable('1. 负载均匀度 @ 随机键', reports);
}

// ── 2. 负载均匀度：顺序结构化键 ───────────────────────────────────────
{
  const reports = HASHES.map((h) =>
    analyzeLoad({
      hashFn: h.fn,
      hashName: h.name,
      nodes: NODES,
      samples: SAMPLES,
      keyGen: sequentialKeyGen(),
      keyType: 'sequential',
      vnodes: VNODES,
    }),
  );
  printLoadTable('2. 负载均匀度 @ 顺序键（key-00000000 …）', reports);
}

// ── 3. 方差曲线：CV 随样本量收敛情况 ──────────────────────────────────
{
  console.log('\n## 3. 方差曲线（CV 实测/理论 随样本量收敛，顺序键）');
  const sizes = [1_000, 10_000, 100_000, 1_000_000];
  console.log(
    `${pad('哈希', 12)}${sizes.map((n) => pad(`N=${(n / 1000).toFixed(0)}k`, 22)).join('')}`,
  );
  console.log(`${pad('', 12)}${sizes.map(() => pad('CV实测  CV比值', 22)).join('')}`);
  console.log('-'.repeat(100));
  for (const h of HASHES) {
    const rows = varianceCurve({
      hashFn: h.fn,
      nodes: NODES,
      sampleSizes: sizes,
      keyGen: sequentialKeyGen(),
      vnodes: VNODES,
    });
    const cells = rows.map(
      (r) => pad(`${(r.cv * 100).toFixed(2)}%  ×${r.cvRatio.toFixed(2)}`, 22),
    );
    console.log(`${pad(h.name, 12)}${cells.join('')}`);
  }
  console.log('注：CV比值 = 实测CV / 结构CV（虚拟节点弧长决定），理想值≈1.00。');
  console.log('    若结构CV本身过大（见第 1 节 CV结构 列），说明哈希连虚拟节点都无法均匀散布。');
}

// ── 4. 位宽扫描：定位雪崩不足暴露的位宽 ───────────────────────────────
{
  const bitsList = [8, 12, 16, 20, 24, 28, 32];
  const sweepSamples = Math.min(SAMPLES, 200_000);
  for (const [idx, mode] of (['low', 'high'] as const).entries()) {
    console.log(
      `\n## 4${'ab'[idx]}. 位宽扫描（取${mode === 'low' ? '低' : '高'} bits 位，顺序键，N=${sweepSamples.toLocaleString('en-US')}）：弧长加权卡方 p 值`,
    );
    console.log(`${pad('哈希', 12)}${bitsList.map((b) => pad(`${b}b`, 11)).join('')}`);
    console.log('-'.repeat(92));
    let collisionRow: number[] = [];
    for (const h of HASHES) {
      const rows = bitWidthCurve({
        hashFn: h.fn,
        nodes: NODES,
        bitsList,
        samples: sweepSamples,
        keyGen: sequentialKeyGen(),
        mode,
        vnodes: VNODES,
      });
      if (collisionRow.length === 0) collisionRow = rows.map((r) => r.ringCollisions);
      const cells = rows.map((r) => {
        const v = verdictOf(r.pValue);
        const mark = v === 'FAIL' ? '✗' : v === 'WARN' ? '!' : '✓';
        return pad(`${fmtP(r.pValue)}${mark}`, 11);
      });
      console.log(`${pad(h.name, 12)}${cells.join('')}`);
    }
    console.log(
      `${pad('环碰撞次数', 12)}${collisionRow.map((c) => pad(String(c), 11)).join('')}`,
    );
  }
  console.log('注：✓=PASS(p≥0.05)  !=WARN(0.01≤p<0.05)  ✗=FAIL(p<0.01，分布显著非均匀)');
  console.log('注：p 值已按各环弧长占比加权；位宽过低时环空间不足、虚拟节点大量碰撞（见碰撞行），');
  console.log('    属于位宽不足的共性结构缺陷，不反映在 p 值中。');
}

// ── 5. 雪崩效应：逐输出位翻转概率偏差 ─────────────────────────────────
{
  console.log('\n## 5. 雪崩效应测试（8 字节输入逐位翻转，每输入位 4000 样本）');
  console.log(
    `${pad('哈希', 12)}${pad('最大偏差', 12)}${pad('缺陷输出位', 40)}${pad('判定', 8)}`,
  );
  console.log('-'.repeat(92));
  for (const h of HASHES) {
    const rep = avalancheReport({ hashFn: h.fn, samplesPerBit: 4000 });
    const verdict = rep.overallMaxDev < 0.05 ? 'PASS' : rep.overallMaxDev < 0.1 ? 'WARN' : 'FAIL';
    const bits =
      rep.deficientBits.length === 0
        ? '无'
        : rep.deficientBits.length > 16
          ? `${rep.deficientBits.length} 个位（bit ${rep.deficientBits[0]}..${rep.deficientBits[rep.deficientBits.length - 1]}）`
          : rep.deficientBits.map((b) => `bit${b}`).join(',');
    console.log(
      `${pad(h.name, 12)}${pad(rep.overallMaxDev.toFixed(4), 12)}${pad(bits, 40)}${pad(verdict, 8)}`,
    );
  }
  console.log('注：理想哈希任意输入位翻转时，每个输出位应以 0.5 概率翻转；偏差>0.1 记为缺陷位。');
}

console.log('\n诊断完成。');
