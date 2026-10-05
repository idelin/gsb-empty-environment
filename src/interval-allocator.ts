/**
 * 区间资源分配器。
 *
 * 语义约定：
 * - 区间为半开区间 [start, end)；start === end 的零长区间不与任何区间重叠。
 * - 两个区间重叠当且仅当 a.start < b.end && b.start < a.end。
 * - 固定绑定（resource 字段）必须保留；同一资源上的固定区间两两重叠时，
 *   直接判定不可行并返回全部冲突对。
 * - 资源编号为非负整数，允许固定编号中存在空洞；新增分配优先填充最小编号
 *   的空洞，以保证“资源总数”最小（总数 = 实际使用的不同编号个数）。
 * - 对其余区间，不能仅按最大重叠数推断可扩展性（区间图上的预染色扩展
 *   问题）：短实例通过穷举（带回溯的 k-染色搜索，k 自下界递增）求得最优；
 *   搜索预算耗尽时退化为 DSATUR 贪心并标记 optimal = false。
 */

export interface IntervalSpec {
  id: string;
  start: number;
  /** 半开区间右端点；允许 end === start（零长区间）。 */
  end: number;
  /** 固定绑定的资源编号（非负整数）；缺省表示待分配。 */
  resource?: number;
}

export interface ConflictPair {
  resource: number;
  a: string;
  b: string;
}

export interface ScheduledInterval {
  id: string;
  start: number;
  end: number;
  fixed: boolean;
}

export interface Allocation {
  /** 固定绑定是否存在同资源重叠冲突；存在时为 false 且不给出分配。 */
  feasible: boolean;
  /** 是否穷举证明了最优；预算耗尽走贪心时为 false。 */
  optimal: boolean;
  /** 固定绑定之间的全部冲突对（按资源号、id 排序）。 */
  conflicts: ConflictPair[];
  /** 实际使用的资源总数（不同编号个数）。 */
  resourceCount: number;
  /** 不依赖搜索的组合下界（扫描线深度 / 固定色数）。 */
  lowerBound: number;
  /** id -> 资源编号。 */
  assignment: ReadonlyMap<string, number>;
  /** 逐资源时间表：资源编号 -> 按 (start, end, id) 排序的区间列表。 */
  schedules: ReadonlyMap<number, readonly ScheduledInterval[]>;
}

export interface AllocateOptions {
  /** 穷举搜索的节点预算，默认 200_000；超出后回退贪心。 */
  maxSearchNodes?: number;
}

const DEFAULT_MAX_SEARCH_NODES = 200_000;

interface NormalizedInterval {
  id: string;
  start: number;
  end: number;
  fixedResource: number | null;
}

function validate(specs: readonly IntervalSpec[]): NormalizedInterval[] {
  const seen = new Set<string>();
  return specs.map((spec, index) => {
    if (typeof spec.id !== "string" || spec.id.length === 0) {
      throw new Error(`intervals[${index}]: id 必须是非空字符串`);
    }
    if (seen.has(spec.id)) {
      throw new Error(`intervals[${index}]: id 重复: ${spec.id}`);
    }
    seen.add(spec.id);
    if (!Number.isFinite(spec.start) || !Number.isFinite(spec.end)) {
      throw new Error(`intervals[${index}] (${spec.id}): start/end 必须是有限数`);
    }
    if (spec.end < spec.start) {
      throw new Error(`intervals[${index}] (${spec.id}): end < start`);
    }
    let fixedResource: number | null = null;
    if (spec.resource !== undefined) {
      if (!Number.isInteger(spec.resource) || spec.resource < 0) {
        throw new Error(
          `intervals[${index}] (${spec.id}): resource 必须是非负整数`,
        );
      }
      fixedResource = spec.resource;
    }
    return { id: spec.id, start: spec.start, end: spec.end, fixedResource };
  });
}

/**
 * 半开区间重叠判定：交集 [max(start), min(end)) 非空才算重叠。
 * 经典写法 a.start < b.end && b.start < a.end 仅在两区间非空时成立；
 * 零长区间（start === end）与一切区间都不重叠。
 */
function overlaps(a: NormalizedInterval, b: NormalizedInterval): boolean {
  return Math.max(a.start, b.start) < Math.min(a.end, b.end);
}

function popcount(mask: bigint): number {
  let count = 0;
  while (mask !== 0n) {
    mask &= mask - 1n;
    count += 1;
  }
  return count;
}

function* bitsOf(mask: bigint): Generator<number> {
  let index = 0;
  while (mask !== 0n) {
    if (mask & 1n) yield index;
    mask >>= 1n;
    index += 1;
  }
}

class BudgetExceeded extends Error {}

/**
 * 预染色 k-染色的穷举判定：MRV 选点 + 前向检查 + 节点预算。
 * 返回每个顶点的调色板下标，或 null（该 k 不可行）。
 */
function exactColoring(
  neighborMasks: readonly bigint[],
  fixedPaletteIndex: readonly (number | null)[],
  k: number,
  budget: { remaining: number },
): number[] | null {
  const n = neighborMasks.length;
  const fullMask = (1n << BigInt(k)) - 1n;
  const domains = new Array<bigint>(n).fill(fullMask);
  const colors = new Array<number>(n).fill(-1);
  const degrees = neighborMasks.map(popcount);

  let assigned = 0;
  for (let v = 0; v < n; v += 1) {
    const fixed = fixedPaletteIndex[v];
    if (fixed !== null) {
      domains[v] = 1n << BigInt(fixed);
      colors[v] = fixed;
      assigned += 1;
    }
  }
  // 固定色向邻居传播一次（固定色此后不再变化）。
  for (let v = 0; v < n; v += 1) {
    const fixed = fixedPaletteIndex[v];
    if (fixed === null) continue;
    const bit = 1n << BigInt(fixed);
    for (const u of bitsOf(neighborMasks[v])) {
      if (colors[u] === -1) {
        domains[u] &= ~bit;
        if (domains[u] === 0n) return null;
      }
    }
  }

  function search(done: number): number[] | null {
    if (done === n) return colors.slice();
    if (budget.remaining <= 0) throw new BudgetExceeded();
    budget.remaining -= 1;

    let pick = -1;
    let pickSize = Infinity;
    let pickDegree = -1;
    for (let v = 0; v < n; v += 1) {
      if (colors[v] !== -1) continue;
      const size = popcount(domains[v]);
      if (size < pickSize || (size === pickSize && degrees[v] > pickDegree)) {
        pick = v;
        pickSize = size;
        pickDegree = degrees[v];
      }
    }
    const v = pick;
    for (const c of bitsOf(domains[v])) {
      const bit = 1n << BigInt(c);
      colors[v] = c;
      const saved: Array<[number, bigint]> = [];
      let ok = true;
      for (const u of bitsOf(neighborMasks[v])) {
        if (colors[u] === -1 && (domains[u] & bit) !== 0n) {
          saved.push([u, domains[u]]);
          domains[u] &= ~bit;
          if (domains[u] === 0n) ok = false;
        }
      }
      if (ok) {
        const result = search(done + 1);
        if (result !== null) return result;
      }
      for (const [u, previous] of saved) domains[u] = previous;
      colors[v] = -1;
    }
    return null;
  }

  return search(assigned);
}

/** DSATUR 贪心：总能给出可行染色，用于预算耗尽后的回退。 */
function greedyColoring(
  adjacency: readonly number[][],
  fixedResource: readonly (number | null)[],
): number[] {
  const n = adjacency.length;
  const colors = new Array<number>(n).fill(-1);
  const used = new Set<number>();
  for (let v = 0; v < n; v += 1) {
    const fixed = fixedResource[v];
    if (fixed !== null) {
      colors[v] = fixed;
      used.add(fixed);
    }
  }
  const neighborColorSets: Array<Set<number>> = adjacency.map(() => new Set());
  let remaining = 0;
  for (let v = 0; v < n; v += 1) {
    if (colors[v] === -1) remaining += 1;
  }

  while (remaining > 0) {
    let pick = -1;
    let pickSat = -1;
    let pickDeg = -1;
    for (let v = 0; v < n; v += 1) {
      if (colors[v] !== -1) continue;
      const sat = neighborColorSets[v].size;
      const deg = adjacency[v].length;
      if (sat > pickSat || (sat === pickSat && deg > pickDeg)) {
        pick = v;
        pickSat = sat;
        pickDeg = deg;
      }
    }
    const v = pick;
    const forbidden = neighborColorSets[v];
    let chosen = -1;
    for (const candidate of [...used].sort((a, b) => a - b)) {
      if (!forbidden.has(candidate)) {
        chosen = candidate;
        break;
      }
    }
    if (chosen === -1) {
      // 启用最小编号的全新资源（优先填洞）。
      let candidate = 0;
      while (used.has(candidate)) candidate += 1;
      chosen = candidate;
    }
    colors[v] = chosen;
    used.add(chosen);
    for (const u of adjacency[v]) {
      if (colors[u] === -1) neighborColorSets[u].add(chosen);
    }
    remaining -= 1;
  }
  return colors;
}

export function allocateResources(
  specs: readonly IntervalSpec[],
  options: AllocateOptions = {},
): Allocation {
  const intervals = validate(specs);
  const n = intervals.length;
  const maxSearchNodes = options.maxSearchNodes ?? DEFAULT_MAX_SEARCH_NODES;

  const adjacency: number[][] = Array.from({ length: n }, () => []);
  const neighborMasks = new Array<bigint>(n).fill(0n);
  for (let i = 0; i < n; i += 1) {
    for (let j = i + 1; j < n; j += 1) {
      if (overlaps(intervals[i], intervals[j])) {
        adjacency[i].push(j);
        adjacency[j].push(i);
        neighborMasks[i] |= 1n << BigInt(j);
        neighborMasks[j] |= 1n << BigInt(i);
      }
    }
  }

  // 固定绑定的同资源重叠：直接给出全部冲突对。
  const conflicts: ConflictPair[] = [];
  for (let i = 0; i < n; i += 1) {
    const ri = intervals[i].fixedResource;
    if (ri === null) continue;
    for (const j of adjacency[i]) {
      if (j > i && intervals[j].fixedResource === ri) {
        conflicts.push({ resource: ri, a: intervals[i].id, b: intervals[j].id });
      }
    }
  }
  conflicts.sort(
    (x, y) =>
      x.resource - y.resource ||
      x.a.localeCompare(y.a) ||
      x.b.localeCompare(y.b),
  );
  if (conflicts.length > 0) {
    return {
      feasible: false,
      optimal: false,
      conflicts,
      resourceCount: 0,
      lowerBound: 0,
      assignment: new Map(),
      schedules: new Map(),
    };
  }

  const fixedResources = intervals
    .map((iv) => iv.fixedResource)
    .filter((r): r is number => r !== null);
  const distinctFixed = new Set(fixedResources);

  // 扫描线下界：任意时刻的（未染色区间数 + 覆盖该点的不同固定色数）
  // 都构成资源总数下界；零长区间不产生覆盖。
  interface SweepEvent {
    position: number;
    delta: number;
    fixedResource: number | null;
  }
  const events: SweepEvent[] = [];
  for (const iv of intervals) {
    if (iv.start === iv.end) continue;
    events.push({ position: iv.start, delta: 1, fixedResource: iv.fixedResource });
    events.push({ position: iv.end, delta: -1, fixedResource: iv.fixedResource });
  }
  events.sort((x, y) => x.position - y.position || y.delta - x.delta);
  let lowerBound = n > 0 ? 1 : 0;
  {
    let uncolored = 0;
    const fixedCoverage = new Map<number, number>();
    let cursor = 0;
    while (cursor < events.length) {
      const position = events[cursor].position;
      while (cursor < events.length && events[cursor].position === position) {
        const event = events[cursor];
        if (event.fixedResource === null) {
          uncolored += event.delta;
        } else {
          const next = (fixedCoverage.get(event.fixedResource) ?? 0) + event.delta;
          if (next === 0) fixedCoverage.delete(event.fixedResource);
          else fixedCoverage.set(event.fixedResource, next);
        }
        cursor += 1;
      }
      lowerBound = Math.max(lowerBound, uncolored + fixedCoverage.size);
    }
  }
  lowerBound = Math.max(lowerBound, distinctFixed.size);

  const fixedResourceList = [...distinctFixed].sort((a, b) => a - b);

  // 调色板：全部固定色 + 最小编号的若干新色（优先填洞），共 k 个。
  function buildPalette(k: number): number[] {
    const palette = [...fixedResourceList];
    const taken = new Set(palette);
    let candidate = 0;
    while (palette.length < k) {
      if (!taken.has(candidate)) {
        palette.push(candidate);
        taken.add(candidate);
      }
      candidate += 1;
    }
    return palette.sort((a, b) => a - b);
  }

  const budget = { remaining: maxSearchNodes };
  let colors: number[] | null = null;
  let optimal = true;
  const upperK = Math.max(lowerBound, n);
  for (let k = lowerBound; k <= upperK; k += 1) {
    const palette = buildPalette(k);
    const paletteIndex = new Map(palette.map((r, i) => [r, i]));
    const fixedPaletteIndex = intervals.map((iv) =>
      iv.fixedResource === null ? null : paletteIndex.get(iv.fixedResource)!,
    );
    let attempt: number[] | null;
    try {
      attempt = exactColoring(neighborMasks, fixedPaletteIndex, k, budget);
    } catch (error) {
      if (error instanceof BudgetExceeded) {
        optimal = false;
        break;
      }
      throw error;
    }
    if (attempt !== null) {
      const paletteResources = palette;
      colors = attempt.map((paletteIdx) => paletteResources[paletteIdx]);
      break;
    }
  }
  if (colors === null) {
    optimal = false;
    colors = greedyColoring(
      adjacency,
      intervals.map((iv) => iv.fixedResource),
    );
  }

  const assignment = new Map<string, number>();
  const schedules = new Map<number, ScheduledInterval[]>();
  intervals.forEach((iv, index) => {
    const resource = colors![index];
    assignment.set(iv.id, resource);
    let list = schedules.get(resource);
    if (!list) {
      list = [];
      schedules.set(resource, list);
    }
    list.push({
      id: iv.id,
      start: iv.start,
      end: iv.end,
      fixed: iv.fixedResource !== null,
    });
  });
  const sortedSchedules = new Map<number, readonly ScheduledInterval[]>(
    [...schedules.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([resource, list]) => [
        resource,
        list.sort(
          (x, y) => x.start - y.start || x.end - y.end || x.id.localeCompare(y.id),
        ),
      ]),
  );

  return {
    feasible: true,
    optimal,
    conflicts: [],
    resourceCount: sortedSchedules.size,
    lowerBound,
    assignment,
    schedules: sortedSchedules,
  };
}
