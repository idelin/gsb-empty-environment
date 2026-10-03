/**
 * LSEQ: 基于无界分数因果树的协同序列标识符分配。
 *
 * 标识符 = 路径分量数组，每个分量为 { digit, site }。
 * 第 d 层（0 起）的 digit 取值区间为 [0, bound(d))，基数随深度自适应翻倍：
 *   bound(d) = 2^(INITIAL_BITS + d)
 * 而 boundary 步长保持常数 —— 正是"基数翻倍 + 步长恒定"使单向密集插入时
 * 每层可容纳的元素数随深度指数增长，标识符深度期望为 O(log n)，避免位宽爆炸。
 *
 * 不变式：终态 digit 恒 >= 1；digit 0 仅作为"下探标记"出现且其后必跟随
 * 更深一层。这保证了任意 prev < next 之间总能确定性地分配出新标识符。
 */

export interface Component {
  digit: number;
  site: number;
}

/** 标识符：从根到叶的分量路径。 */
export type Identifier = Component[];

export const INITIAL_BITS = 4; // 第一层基数 2^4 = 16
export const BOUNDARY = 8; // 固定随机步长上界（LSEQ 的 boundary 参数）

/** 第 depth 层的 digit 上界（不含）。 */
export function bound(depth: number): number {
  return 2 ** (INITIAL_BITS + depth);
}

/** 每层随机选择 boundary+ / boundary- 策略（站点级持久，保证确定性）。 */
export class StrategyTable {
  private strategies = new Map<number, boolean>(); // true => boundary+
  private readonly random: () => number;

  constructor(random: () => number = Math.random) {
    this.random = random;
  }

  isBoundaryPlus(depth: number): boolean {
    let s = this.strategies.get(depth);
    if (s === undefined) {
      s = this.random() < 0.5;
      this.strategies.set(depth, s);
    }
    return s;
  }
}

function randomInt(random: () => number, min: number, max: number): number {
  // 闭区间 [min, max]
  return min + Math.floor(random() * (max - min + 1));
}

function componentLess(a: Component, b: Component): boolean {
  return a.digit !== b.digit ? a.digit < b.digit : a.site < b.site;
}

/**
 * 在 prev 与 next 之间分配一个新标识符（prev < result < next）。
 * prev/next 为 null 分别表示序列首/尾的虚拟边界。
 */
export function allocate(
  prev: Identifier | null,
  next: Identifier | null,
  site: number,
  strategies: StrategyTable,
  random: () => number = Math.random,
): Identifier {
  const id: Identifier = [];
  for (let depth = 0; ; depth++) {
    const pC = prev !== null && depth < prev.length ? prev[depth] : null;
    const nC = next !== null && depth < next.length ? next[depth] : null;
    const loDigit = pC ? pC.digit : -1; // -1 为虚拟下界，从不出现在结果中
    const hiDigit = nC ? nC.digit : bound(depth); // bound(d) 为虚拟上界

    const minDigit = Math.max(loDigit + 1, 1); // 终态 digit 必须 >= 1
    const maxDigit = hiDigit - 1;

    if (minDigit <= maxDigit) {
      // 本层有空位：按 boundary± 策略取恒定宽度的随机步长，生成致密标识符
      const width = Math.min(BOUNDARY, maxDigit - minDigit + 1);
      const step = randomInt(random, 1, width);
      const digit = strategies.isBoundaryPlus(depth)
        ? minDigit + step - 1
        : maxDigit - step + 1;
      id.push({ digit, site });
      return id;
    }

    if (pC !== null) {
      // 本层无空位：继承 prev 的分量（保持 prev < result），深入下一层。
      // 若 pC 严格小于 nC，则 result < next 在本层即已确定。
      id.push(pC);
      if (nC === null || componentLess(pC, nC)) next = null;
      continue;
    }

    // prev 已耗尽（result > prev 已自动成立），只剩 next 的约束。
    if (hiDigit >= 2) {
      // 上面 minDigit<=maxDigit 分支已覆盖，防御性保留。
      const digit = randomInt(random, 1, hiDigit - 1);
      id.push({ digit, site });
      return id;
    }
    if (hiDigit === 1) {
      // 唯一可选 digit 是 0：以 0 作为下探标记（非终态），下一层必有完整空间。
      id.push({ digit: 0, site });
      next = null; // 0 < 1，result < next 已确定
      continue;
    }
    // hiDigit === 0：next 在本层也是下探标记，复制其分量继续下探；
    // 因终态 digit 恒 >= 1，next 的零标记链有限，递归必终止。
    id.push(nC!);
  }
}

/** 字典序比较：先比 digit，同 digit 比 site（保证全序且确定）。 */
export function compare(a: Identifier, b: Identifier): number {
  const len = Math.min(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i].digit !== b[i].digit) return a[i].digit - b[i].digit;
    if (a[i].site !== b[i].site) return a[i].site - b[i].site;
  }
  return a.length - b.length;
}

/** 标识符深度（路径长度）。 */
export function depth(id: Identifier): number {
  return id.length;
}

/** 标识符占用的总位宽（digit 位宽 + site 位宽），用于衡量位宽膨胀。 */
export function bitWidth(id: Identifier): number {
  let bits = 0;
  for (let d = 0; d < id.length; d++) {
    bits += INITIAL_BITS + d; // digit 位宽
    bits += 32; // site id 固定位宽
  }
  return bits;
}

/** 可读的分数形式，如 "3@2/12@5"。 */
export function format(id: Identifier): string {
  return id.map((c) => `${c.digit}@${c.site}`).join('/');
}
