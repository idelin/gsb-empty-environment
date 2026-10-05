/**
 * 无约束二值特性两两覆盖的组合下界（Kleitman–Spencer 定理的精确计算版）。
 *
 * N 个配置（行）覆盖 k 个特性的全部两两取值组合，当且仅当对应的 k 个
 * {0,1}^N 列两两"定性独立"：对任意两列 A、B（视为行集合 [N] 的子集），
 * 00/01/10/11 四种组合都出现 ⟺ A∩B≠∅ 且 A\B≠∅ 且 B\A≠∅ 且 A∪B≠[N]。
 *
 * 于是"N 行最多能容纳多少列"= 相容图上的最大团。这里用 Tomita 算法精确求解
 * （N≤8 时顶点数 ≤254，瞬间完成），结果同时充当：
 *   1) 下界证明：若 maxCols(N-1) < k，则 N-1 个配置不可能覆盖 k 个特性；
 *   2) 上界构造：最大团本身即一组可用列。
 */

/** 判断两个列（行子集位掩码）是否定性独立。 */
function compatible(a: number, b: number, full: number): boolean {
  return (a & b) !== 0 && (a & ~b & full) !== 0 && (b & ~a & full) !== 0 && ((a | b) !== full);
}

function lowestBit(x: bigint): bigint {
  return x & -x;
}

function bitIndex(b: bigint): number {
  // b 是 2 的幂
  let i = 0;
  while (b > 1n) { b >>= 1n; i++; }
  return i;
}

const cache = new Map<number, number[]>();

/** N 行上两两定性独立的最大列族（最大团），返回列（行子集位掩码）数组。 */
export function maxQIColumns(n: number): number[] {
  const hit = cache.get(n);
  if (hit) return hit;

  const full = (1 << n) - 1;
  const verts: number[] = [];
  for (let s = 1; s < full; s++) verts.push(s); // 空集与全集不可能与任何列相容

  const adj = new Map<number, bigint>();
  for (const a of verts) {
    let bits = 0n;
    for (const b of verts) {
      if (a !== b && compatible(a, b, full)) bits |= 1n << BigInt(b);
    }
    adj.set(a, bits);
  }

  let best: number[] = [];

  // Tomita 最大团（带贪心着色上界）
  function expand(r: number[], p: bigint): void {
    if (p === 0n) {
      if (r.length > best.length) best = r.slice();
      return;
    }
    // 贪心着色，得到每个候选的颜色上界
    const order: number[] = [];
    const colorBound: number[] = [];
    let uncolored = p;
    let color = 0;
    while (uncolored !== 0n) {
      color++;
      let avail = uncolored;
      while (avail !== 0n) {
        const vBit = lowestBit(avail);
        const v = bitIndex(vBit);
        order.push(v);
        colorBound.push(color);
        uncolored &= ~vBit;
        avail &= ~vBit;
        avail &= ~adj.get(v)!;
      }
    }
    let cand = p;
    for (let i = order.length - 1; i >= 0; i--) {
      if (r.length + colorBound[i] <= best.length) return;
      const v = order[i];
      const vBit = 1n << BigInt(v);
      if ((cand & vBit) === 0n) continue;
      expand([...r, v], cand & adj.get(v)!);
      cand &= ~vBit;
    }
  }

  let initial = 0n;
  for (const v of verts) initial |= 1n << BigInt(v);
  expand([], initial);

  cache.set(n, best);
  return best;
}

/**
 * 覆盖 k 个无约束二值特性所需的最少配置数 N，
 * 以及达到该下界的列构造（每列是 N 行的位掩码）。
 */
export function minRowsUnconstrained(k: number): { rows: number; columns: number[] } {
  for (let n = 2; ; n++) {
    const cols = maxQIColumns(n);
    if (cols.length >= k) {
      return { rows: n, columns: cols.slice(0, k) };
    }
  }
}
