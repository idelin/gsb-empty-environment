import type { HashFn, Key, TruncateMode } from './hashes.ts';
import { truncateBits } from './hashes.ts';
import { HashRing } from './ring.ts';
import { mean, stdev, chiSquarePValue } from './stats.ts';

export type KeyGen = (index: number) => Key;

/** 确定性 PRNG，保证报告与测试可复现 */
export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** 随机十六进制字符串键生成器（种子固定，可复现） */
export function randomKeyGen(seed = 0xc0ffee): KeyGen {
  const rand = mulberry32(seed);
  return () => {
    const hi = Math.floor(rand() * 0x100000000);
    const lo = Math.floor(rand() * 0x100000000);
    return hi.toString(16).padStart(8, '0') + lo.toString(16).padStart(8, '0');
  };
}

/** 顺序结构化键生成器（业务中最常见的键形态，也是弱哈希的照妖镜） */
export function sequentialKeyGen(prefix = 'key-'): KeyGen {
  return (i) => `${prefix}${i.toString().padStart(8, '0')}`;
}

export interface LoadReport {
  hashName: string;
  keyType: string;
  nodeCount: number;
  samples: number;
  vnodes: number;
  counts: Record<string, number>;
  min: number;
  max: number;
  mean: number;
  stddev: number;
  /** 实测变异系数 = stddev / mean */
  cv: number;
  /** 环结构（虚拟节点弧长占比）决定的理论负载 CV，均匀哈希的实测 CV 应收敛于此 */
  structuralCv: number;
  /** 纯采样噪声 CV = sqrt((k-1)/N) */
  samplingCv: number;
  /** 实测 CV / 结构 CV，理想值 ≈ 1，越大说明哈希映射本身越不均匀 */
  cvRatio: number;
  maxMinRatio: number;
  /** 弧长加权卡方：检验“键在环空间上均匀分布”（已剔除环结构因素） */
  chi2: number;
  df: number;
  pValue: number;
  /** 相对绝对均匀的卡方（含环结构不平衡，仅供对照） */
  uniformChi2: number;
  uniformPValue: number;
  verdict: 'PASS' | 'WARN' | 'FAIL';
  ringCollisions: number;
}

export function verdictOf(pValue: number, structuralCv = 0): 'PASS' | 'WARN' | 'FAIL' {
  // 取“键映射卡方”与“环结构 CV”两者中更差的判定
  const byP = pValue < 0.01 ? 2 : pValue < 0.05 ? 1 : 0;
  const byStructure = structuralCv > 0.5 ? 2 : structuralCv > 0.25 ? 1 : 0;
  return (['PASS', 'WARN', 'FAIL'] as const)[Math.max(byP, byStructure)];
}

export function countSamples(
  ring: HashRing,
  samples: number,
  keyGen: KeyGen,
): Map<string, number> {
  const counts = new Map<string, number>();
  for (let i = 0; i < samples; i++) {
    const node = ring.getNode(keyGen(i));
    counts.set(node, (counts.get(node) ?? 0) + 1);
  }
  return counts;
}

function chiSquareAgainstExpected(values: number[], expected: number[]): number {
  let chi2 = 0;
  for (let i = 0; i < values.length; i++) {
    if (expected[i] === 0) {
      if (values[i] > 0) return Infinity; // 零期望桶出现样本：直接判死
      continue;
    }
    const d = values[i] - expected[i];
    chi2 += (d * d) / expected[i];
  }
  return chi2;
}

export function buildReport(
  hashName: string,
  keyType: string,
  nodes: string[],
  samples: number,
  vnodes: number,
  counts: Map<string, number>,
  ring: HashRing,
): LoadReport {
  const values = nodes.map((n) => counts.get(n) ?? 0);
  const m = mean(values);
  const sd = stdev(values);
  const cv = m === 0 ? Infinity : sd / m;

  const arcs = ring.arcFractions();
  const fractions = nodes.map((n) => arcs.get(n) ?? 0);
  const arcMean = mean(fractions);
  const structuralCv = arcMean === 0 ? Infinity : stdev(fractions) / arcMean;

  const expected = fractions.map((f) => f * samples);
  const chi2 = chiSquareAgainstExpected(values, expected);
  const df = nodes.length - 1;
  const pValue = chi2 === Infinity ? 0 : chiSquarePValue(chi2, df);

  const uniformExpected = samples / nodes.length;
  const uniformChi2 = chiSquareAgainstExpected(
    values,
    nodes.map(() => uniformExpected),
  );
  const uniformPValue = chiSquarePValue(uniformChi2, df);

  const min = Math.min(...values);
  const max = Math.max(...values);
  const countsRecord: Record<string, number> = {};
  for (const n of nodes) countsRecord[n] = counts.get(n) ?? 0;
  return {
    hashName,
    keyType,
    nodeCount: nodes.length,
    samples,
    vnodes,
    counts: countsRecord,
    min,
    max,
    mean: m,
    stddev: sd,
    cv,
    structuralCv,
    samplingCv: Math.sqrt(df / samples),
    cvRatio: structuralCv === 0 ? Infinity : cv / structuralCv,
    maxMinRatio: min === 0 ? Infinity : max / min,
    chi2,
    df,
    pValue,
    uniformChi2,
    uniformPValue,
    verdict: verdictOf(pValue, structuralCv),
    ringCollisions: ring.stats.collisions,
  };
}

export interface LoadOptions {
  hashFn: HashFn;
  hashName: string;
  nodes: string[];
  samples: number;
  keyGen: KeyGen;
  keyType?: string;
  vnodes?: number;
}

/** 负载均匀度分析：打点 -> 标准差 / 最大最小比 / 弧长加权卡方拟合优度 */
export function analyzeLoad(opts: LoadOptions): LoadReport {
  const vnodes = opts.vnodes ?? 160;
  const keyType = opts.keyType ?? 'random';
  const ring = new HashRing(opts.nodes, opts.hashFn, vnodes);
  const counts = countSamples(ring, opts.samples, opts.keyGen);
  return buildReport(opts.hashName, keyType, opts.nodes, opts.samples, vnodes, counts, ring);
}

export interface VarianceCurveRow {
  samples: number;
  cv: number;
  structuralCv: number;
  samplingCv: number;
  /** 实测 CV / 结构 CV，理想值 ≈ 1 */
  cvRatio: number;
  maxMinRatio: number;
  chi2: number;
  pValue: number;
}

/** 方差曲线：随样本量增长，实测 CV 应收敛到环结构 CV（cvRatio → 1） */
export function varianceCurve(opts: {
  hashFn: HashFn;
  nodes: string[];
  sampleSizes: number[];
  keyGen: KeyGen;
  vnodes?: number;
}): VarianceCurveRow[] {
  const vnodes = opts.vnodes ?? 160;
  const ring = new HashRing(opts.nodes, opts.hashFn, vnodes);
  const rows: VarianceCurveRow[] = [];
  for (const samples of opts.sampleSizes) {
    const counts = countSamples(ring, samples, opts.keyGen);
    const report = buildReport('', '', opts.nodes, samples, vnodes, counts, ring);
    rows.push({
      samples,
      cv: report.cv,
      structuralCv: report.structuralCv,
      samplingCv: report.samplingCv,
      cvRatio: report.cvRatio,
      maxMinRatio: report.maxMinRatio,
      chi2: report.chi2,
      pValue: report.pValue,
    });
  }
  return rows;
}

export interface BitWidthRow {
  bits: number;
  mode: TruncateMode;
  pValue: number;
  chi2: number;
  cv: number;
  structuralCv: number;
  cvRatio: number;
  maxMinRatio: number;
  ringCollisions: number;
  verdict: 'PASS' | 'WARN' | 'FAIL';
}

/**
 * 位宽扫描：把哈希输出截断到 bits 位后建环打点，
 * 卡方按各截断位宽环的弧长占比加权，精确定位雪崩不足开始暴露的位宽区间。
 * 键的完整哈希值只计算一次，各位宽复用（掩码截断）。
 */
export function bitWidthCurve(opts: {
  hashFn: HashFn;
  nodes: string[];
  bitsList: number[];
  samples: number;
  keyGen: KeyGen;
  mode?: TruncateMode;
  vnodes?: number;
}): BitWidthRow[] {
  const vnodes = opts.vnodes ?? 160;
  const mode = opts.mode ?? 'low';

  const fullHashes = new Uint32Array(opts.samples);
  for (let i = 0; i < opts.samples; i++) {
    fullHashes[i] = opts.hashFn(opts.keyGen(i));
  }

  const rows: BitWidthRow[] = [];
  for (const bits of opts.bitsList) {
    const truncated = truncateBits(opts.hashFn, bits, mode);
    const ring = new HashRing(opts.nodes, truncated, vnodes, 2 ** bits);
    const mask = bits === 32 ? 0xffffffff : (1 << bits) - 1;
    const shift = 32 - bits;
    const counts = new Map<string, number>();
    for (let i = 0; i < opts.samples; i++) {
      // 注意：& 运算会强制转 int32，bits=32 时必须 >>> 0 回到无符号
      const pos = mode === 'low' ? (fullHashes[i] & mask) >>> 0 : fullHashes[i] >>> shift;
      const node = ring.getNodeByPosition(pos);
      counts.set(node, (counts.get(node) ?? 0) + 1);
    }
    const report = buildReport('', '', opts.nodes, opts.samples, vnodes, counts, ring);
    rows.push({
      bits,
      mode,
      pValue: report.pValue,
      chi2: report.chi2,
      cv: report.cv,
      structuralCv: report.structuralCv,
      cvRatio: report.cvRatio,
      maxMinRatio: report.maxMinRatio,
      ringCollisions: ring.stats.collisions,
      verdict: report.verdict,
    });
  }
  return rows;
}

export interface AvalancheBitRow {
  outputBit: number;
  meanP: number;
  maxDev: number;
  worstInputBit: number;
}

export interface AvalancheReport {
  inputBits: number;
  samplesPerBit: number;
  rows: AvalancheBitRow[];
  /** 全部输出位中 |P(翻转)-0.5| 的最大值，理想哈希应 < 0.05 */
  overallMaxDev: number;
  /** 偏差超过阈值（默认 0.1）的输出位列表 */
  deficientBits: number[];
}

/**
 * 雪崩效应测试：逐位翻转输入，统计每个输出位的翻转概率。
 * 理想哈希每个输出位都应以 0.5 的概率翻转；
 * FNV 类乘法哈希的高位输出对输入变化不敏感，会在此暴露。
 */
export function avalancheReport(opts: {
  hashFn: HashFn;
  inputBytes?: number;
  samplesPerBit?: number;
  seed?: number;
  threshold?: number;
}): AvalancheReport {
  const inputBytes = opts.inputBytes ?? 8;
  const samplesPerBit = opts.samplesPerBit ?? 200;
  const threshold = opts.threshold ?? 0.1;
  const inputBits = inputBytes * 8;
  const rand = mulberry32(opts.seed ?? 0x5eed);

  const flips: number[][] = Array.from({ length: inputBits }, () =>
    new Array<number>(32).fill(0),
  );
  const key = new Uint8Array(inputBytes);

  for (let bit = 0; bit < inputBits; bit++) {
    const byteIdx = bit >>> 3;
    const bitMask = 1 << (bit & 7);
    for (let s = 0; s < samplesPerBit; s++) {
      for (let b = 0; b < inputBytes; b++) key[b] = Math.floor(rand() * 256);
      const h1 = opts.hashFn(key);
      key[byteIdx] ^= bitMask;
      const h2 = opts.hashFn(key);
      let diff = (h1 ^ h2) >>> 0;
      for (let j = 0; j < 32; j++) {
        if (diff & 1) flips[bit][j]++;
        diff >>>= 1;
      }
    }
  }

  const rows: AvalancheBitRow[] = [];
  let overallMaxDev = 0;
  const deficientBits: number[] = [];
  for (let j = 0; j < 32; j++) {
    let sum = 0;
    let maxDev = 0;
    let worstInputBit = 0;
    for (let bit = 0; bit < inputBits; bit++) {
      const p = flips[bit][j] / samplesPerBit;
      sum += p;
      const dev = Math.abs(p - 0.5);
      if (dev > maxDev) {
        maxDev = dev;
        worstInputBit = bit;
      }
    }
    if (maxDev > overallMaxDev) overallMaxDev = maxDev;
    if (maxDev > threshold) deficientBits.push(j);
    rows.push({ outputBit: j, meanP: sum / inputBits, maxDev, worstInputBit });
  }
  return { inputBits, samplesPerBit, rows, overallMaxDev, deficientBits };
}
