// 规范霍夫曼码：按 (码长, 符号) 排序建立；拒绝过度订阅，允许欠订阅。
import { FormatError } from './errors.ts';
export const MAX_CODE_LENGTH = 15;
export const NUM_SYMBOLS = 286;


// 由符号频率构建限长（≤ maxLen）霍夫曼码长表。
// 深度超限时用 zlib 式溢出修正；最后以 Kraft 校验兜底，异常时退化为等长码。
export function buildCodeLengths(
  freqs: ArrayLike<number>,
  maxLen: number = MAX_CODE_LENGTH,
): Uint8Array {
  const n = freqs.length;
  const result = new Uint8Array(n);
  const used: number[] = [];
  for (let s = 0; s < n; s++) {
    if (freqs[s] > 0) used.push(s);
  }
  if (used.length === 0) return result;
  if (used.length === 1) {
    result[used[0]] = 1;
    return result;
  }

  // 霍夫曼树：反复合并频率最小的两个无父节点（n ≤ 286，O(n^2) 足够）。
  const nodeFreq: number[] = [];
  const parent: number[] = [];
  for (const s of used) {
    nodeFreq.push(freqs[s]);
    parent.push(-1);
  }
  let nodeCount = used.length;
  for (;;) {
    let m1 = -1;
    let m2 = -1;
    for (let i = 0; i < nodeCount; i++) {
      if (parent[i] !== -1) continue;
      if (m1 === -1 || nodeFreq[i] < nodeFreq[m1]) {
        m2 = m1;
        m1 = i;
      } else if (m2 === -1 || nodeFreq[i] < nodeFreq[m2]) {
        m2 = i;
      }
    }
    if (m2 === -1) break;
    nodeFreq.push(nodeFreq[m1] + nodeFreq[m2]);
    parent.push(-1);
    parent[m1] = nodeCount;
    parent[m2] = nodeCount;
    nodeCount++;
  }

  // 统计各符号深度，超限的钳到 maxLen。
  const blCount = new Array<number>(maxLen + 1).fill(0);
  for (let i = 0; i < used.length; i++) {
    let depth = 0;
    for (let j = i; parent[j] !== -1; j = parent[j]) depth++;
    if (depth === 0) depth = 1;
    if (depth > maxLen) depth = maxLen;
    blCount[depth]++;
  }

  // 限长修正：以 2^-maxLen 为单位的 Kraft 和超过 1 时，
  // 反复把“最长的短码”降一级（K 严格递减且为整数，必然终止；
  // 候选必然存在，否则全部叶子都在 maxLen，K = 叶子数 ≤ 2^maxLen）。
  const cap = 2 ** maxLen;
  let kraft = 0;
  for (let l = 1; l <= maxLen; l++) kraft += blCount[l] * 2 ** (maxLen - l);
  while (kraft > cap) {
    let bits = maxLen - 1;
    while (bits > 0 && blCount[bits] === 0) bits--;
    if (bits === 0) return balancedLengths(used, n); // 理论兜底，不应到达
    blCount[bits]--;
    blCount[bits + 1]++;
    kraft -= 2 ** (maxLen - bits - 1);
  }

  // 修复：在不超上限的前提下把过长的码提升一级，尽量恢复完备码（Kraft = 1）。
  // 叶子总数不变；提升最长叶子每次只增加最小的 Kraft 增量。
  while (kraft < cap) {
    let bits = maxLen;
    while (bits > 1 && blCount[bits] === 0) bits--;
    if (bits <= 1) break;
    const delta = 2 ** (maxLen - bits);
    if (kraft + delta > cap) break; // 无法恰好补满则保持欠订阅（解码器允许）
    blCount[bits]--;
    blCount[bits - 1]++;
    kraft += delta;
  }

  // 重新分配：频率最低的符号拿最长的码。
  const order = [...used].sort((a, b) => freqs[a] - freqs[b] || a - b);
  let idx = 0;
  for (let len = maxLen; len >= 1; len--) {
    for (let k = 0; k < blCount[len]; k++) {
      result[order[idx++]] = len;
    }
  }

  if (!isKraftValid(result)) {
    return balancedLengths(used, n); // 兜底，保证返回合法码
  }
  return result;
}

// 等长码兜底：k = ceil(log2(符号数))，Kraft 必然 ≤ 1。
function balancedLengths(used: number[], n: number): Uint8Array {
  const result = new Uint8Array(n);
  const len = Math.max(1, Math.ceil(Math.log2(used.length)));
  for (const s of used) result[s] = len;
  return result;
}

export function isKraftValid(lengths: ArrayLike<number>): boolean {
  let kraft = 0;
  for (let i = 0; i < lengths.length; i++) {
    if (lengths[i] > 0) kraft += 2 ** (MAX_CODE_LENGTH - lengths[i]);
  }
  return kraft <= 2 ** MAX_CODE_LENGTH;
}

// 校验码长表：码长 0..15、至少一个非零、不过度订阅（欠订阅允许）。
export function validateCodeLengths(lengths: ArrayLike<number>): void {
  let used = 0;
  let kraft = 0;
  for (let i = 0; i < lengths.length; i++) {
    const len = lengths[i];
    if (!Number.isInteger(len) || len < 0 || len > MAX_CODE_LENGTH) {
      throw new FormatError(`非法码长: 符号 ${i} 码长 ${len}`);
    }
    if (len > 0) {
      used++;
      kraft += 2 ** (MAX_CODE_LENGTH - len);
    }
  }
  if (used === 0) {
    throw new FormatError('码长表为空');
  }
  if (kraft > 2 ** MAX_CODE_LENGTH) {
    throw new FormatError('过度订阅的霍夫曼码表');
  }
}

// 由码长表计算规范码字（编码端使用）。调用前必须先通过 validateCodeLengths。
export function canonicalCodes(lengths: ArrayLike<number>): Uint32Array {
  const blCount = new Array<number>(MAX_CODE_LENGTH + 1).fill(0);
  for (let i = 0; i < lengths.length; i++) {
    if (lengths[i] > 0) blCount[lengths[i]]++;
  }
  const nextCode = new Array<number>(MAX_CODE_LENGTH + 1).fill(0);
  let code = 0;
  for (let bits = 1; bits <= MAX_CODE_LENGTH; bits++) {
    code = (code + blCount[bits - 1]) << 1;
    nextCode[bits] = code;
  }
  const codes = new Uint32Array(lengths.length);
  for (let s = 0; s < lengths.length; s++) {
    const len = lengths[s];
    if (len > 0) codes[s] = nextCode[len]++;
  }
  return codes;
}

// 增量规范码解码器：逐位读取，位型非法时抛 FormatError。
// readBit 返回 null 表示输入暂时不足（调用方负责位位置回滚）。
export class CanonicalDecoder {
  private counts: Uint16Array;
  private offsets: Uint16Array;
  private symbols: Uint16Array;
  private firstCode: Uint32Array;

  constructor(lengths: ArrayLike<number>) {
    validateCodeLengths(lengths);
    const counts = new Uint16Array(MAX_CODE_LENGTH + 1);
    for (let i = 0; i < lengths.length; i++) {
      if (lengths[i] > 0) counts[lengths[i]]++;
    }
    const offsets = new Uint16Array(MAX_CODE_LENGTH + 1);
    let total = 0;
    for (let len = 1; len <= MAX_CODE_LENGTH; len++) {
      offsets[len] = total;
      total += counts[len];
    }
    const symbols = new Uint16Array(total);
    const fill = Uint16Array.from(offsets);
    for (let s = 0; s < lengths.length; s++) {
      const len = lengths[s];
      if (len > 0) symbols[fill[len]++] = s;
    }
    const firstCode = new Uint32Array(MAX_CODE_LENGTH + 1);
    let code = 0;
    for (let len = 1; len <= MAX_CODE_LENGTH; len++) {
      code = (code + counts[len - 1]) << 1;
      firstCode[len] = code;
    }
    this.counts = counts;
    this.offsets = offsets;
    this.symbols = symbols;
    this.firstCode = firstCode;
  }

  decode(readBit: () => number | null): number | null {
    let code = 0;
    for (let len = 1; len <= MAX_CODE_LENGTH; len++) {
      const bit = readBit();
      if (bit === null) return null;
      code = (code << 1) | bit;
      const first = this.firstCode[len];
      if (code < first) {
        throw new FormatError('非法霍夫曼码字');
      }
      const idx = code - first;
      if (idx < this.counts[len]) {
        return this.symbols[this.offsets[len] + idx];
      }
    }
    throw new FormatError('非法霍夫曼码字: 超过最大码长');
  }
}
