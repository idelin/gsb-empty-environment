/**
 * 范围规则规范化工具
 *
 * 输入:一组带优先级的规则,每条规则把一个整数多维盒(1~3 维,半开区间 [lo, hi))
 * 映射到一个标签。盒之间允许重叠,重叠处按优先级决胜;优先级相同则取标签
 * 字典序较小者(确定性规则,保证与输入顺序无关)。
 *
 * 输出:互不重叠的规范分区(带标签的盒列表)。相邻同标签区域被合并,
 * 且合并结果只取决于规则集合本身,与规则的输入顺序无关。
 *
 * 方法:坐标压缩得到基本单元网格 -> 每个单元按优先级确定唯一胜出的标签 ->
 * 每个标签的单元集合按"字典序最小剩余单元 + 按维度顺序贪心扩张"分解为盒。
 * 单元集合与决胜规则都是顺序无关的,贪心分解是单元集合的确定性函数,
 * 因此整个流水线与输入顺序无关。
 */

export interface Box {
  lo: number[];
  hi: number[];
}

export interface Rule {
  box: Box;
  label: string;
  priority: number;
}

export interface Region {
  box: Box;
  label: string;
}

export interface VerifyReport {
  ok: boolean;
  disjoint: boolean;
  semanticsMatch: boolean;
  regionVolume: number;
  coveredVolume: number;
  errors: string[];
}

function checkBox(box: Box, dims: number, ctx: string): void {
  if (
    !Array.isArray(box.lo) ||
    !Array.isArray(box.hi) ||
    box.lo.length !== dims ||
    box.hi.length !== dims
  ) {
    throw new Error(`${ctx}: expected a ${dims}D box {lo, hi}`);
  }
  for (let d = 0; d < dims; d++) {
    const lo = box.lo[d];
    const hi = box.hi[d];
    if (!Number.isInteger(lo) || !Number.isInteger(hi)) {
      throw new Error(`${ctx}: coordinates must be integers (dim ${d})`);
    }
    if (!(lo < hi)) {
      throw new Error(`${ctx}: require lo < hi on dim ${d}, got [${lo}, ${hi})`);
    }
  }
}

export function boxVolume(box: Box): number {
  let v = 1;
  for (let d = 0; d < box.lo.length; d++) v *= box.hi[d] - box.lo[d];
  return v;
}

function boxesOverlap(a: Box, b: Box): boolean {
  for (let d = 0; d < a.lo.length; d++) {
    if (a.lo[d] >= b.hi[d] || b.lo[d] >= a.hi[d]) return false;
  }
  return true;
}

function buildCoords(rules: Rule[], dims: number): number[][] {
  const coords: number[][] = [];
  for (let d = 0; d < dims; d++) {
    const values = new Set<number>();
    for (const r of rules) {
      values.add(r.box.lo[d]);
      values.add(r.box.hi[d]);
    }
    coords.push([...values].sort((x, y) => x - y));
  }
  return coords;
}

/** 优先级高者胜;优先级相同取标签字典序较小者 —— 与输入顺序无关。 */
function beats(a: Rule, b: Rule): boolean {
  if (a.priority !== b.priority) return a.priority > b.priority;
  return a.label < b.label;
}

function winnerAt(rules: Rule[], lo: number[], hi: number[]): Rule | null {
  let best: Rule | null = null;
  for (const r of rules) {
    let covers = true;
    for (let d = 0; d < lo.length; d++) {
      if (r.box.lo[d] > lo[d] || r.box.hi[d] < hi[d]) {
        covers = false;
        break;
      }
    }
    if (covers && (best === null || beats(r, best))) best = r;
  }
  return best;
}

function winnerAtPoint(rules: Rule[], point: number[]): Rule | null {
  let best: Rule | null = null;
  for (const r of rules) {
    let covers = true;
    for (let d = 0; d < point.length; d++) {
      if (r.box.lo[d] > point[d] || point[d] >= r.box.hi[d]) {
        covers = false;
        break;
      }
    }
    if (covers && (best === null || beats(r, best))) best = r;
  }
  return best;
}

function cellKey(idx: number[], counts: number[]): number {
  let key = 0;
  let stride = 1;
  for (let d = 0; d < idx.length; d++) {
    key += idx[d] * stride;
    stride *= counts[d];
  }
  return key;
}

function forEachIndex(counts: number[], fn: (idx: number[]) => void): void {
  const dims = counts.length;
  const idx = new Array<number>(dims).fill(0);
  for (;;) {
    fn(idx.slice());
    let d = 0;
    while (d < dims) {
      idx[d] += 1;
      if (idx[d] < counts[d]) break;
      idx[d] = 0;
      d += 1;
    }
    if (d === dims) return;
  }
}

function forEachInRange(start: number[], end: number[], fn: (idx: number[]) => void): void {
  const dims = start.length;
  const idx = start.slice();
  for (;;) {
    fn(idx.slice());
    let d = 0;
    while (d < dims) {
      idx[d] += 1;
      if (idx[d] <= end[d]) break;
      idx[d] = start[d];
      d += 1;
    }
    if (d === dims) return;
  }
}

function compareIdx(a: number[], b: number[]): number {
  for (let d = 0; d < a.length; d++) {
    if (a[d] !== b[d]) return a[d] - b[d];
  }
  return 0;
}

function slabPresent(
  start: number[],
  end: number[],
  dim: number,
  at: number,
  present: Set<number>,
  counts: number[],
): boolean {
  const lo = start.slice();
  const hi = end.slice();
  lo[dim] = at;
  hi[dim] = at;
  let ok = true;
  forEachInRange(lo, hi, (idx) => {
    if (!present.has(cellKey(idx, counts))) ok = false;
  });
  return ok;
}

/**
 * 把同一标签的基本单元集合分解为互不相交的盒。
 * 每次取字典序最小的剩余单元,按维度 0..D-1 的顺序尽量贪心扩张。
 * 这是单元集合的确定性函数,不依赖任何遍历/输入顺序。
 */
function growBoxes(
  sortedCells: number[][],
  present: Set<number>,
  counts: number[],
  coords: number[][],
): Box[] {
  const dims = counts.length;
  const boxes: Box[] = [];
  let ptr = 0;
  for (;;) {
    while (ptr < sortedCells.length && !present.has(cellKey(sortedCells[ptr], counts))) ptr += 1;
    if (ptr >= sortedCells.length) break;
    const start = sortedCells[ptr].slice();
    const end = sortedCells[ptr].slice();
    for (let d = 0; d < dims; d++) {
      for (;;) {
        const next = end[d] + 1;
        if (next >= counts[d]) break;
        if (!slabPresent(start, end, d, next, present, counts)) break;
        end[d] = next;
      }
    }
    const lo: number[] = [];
    const hi: number[] = [];
    for (let d = 0; d < dims; d++) {
      lo.push(coords[d][start[d]]);
      hi.push(coords[d][end[d] + 1]);
    }
    boxes.push({ lo, hi });
    forEachInRange(start, end, (idx) => present.delete(cellKey(idx, counts)));
  }
  return boxes;
}

/** 规范化:规则集合 -> 互不重叠、与输入顺序无关的带标签分区。 */
export function normalize(rules: Rule[]): Region[] {
  if (rules.length === 0) return [];
  const dims = rules[0].box.lo.length;
  if (dims < 1 || dims > 3) {
    throw new Error(`only 1..3 dimensions are supported, got ${dims}`);
  }
  rules.forEach((r, i) => checkBox(r.box, dims, `rule[${i}]`));

  const coords = buildCoords(rules, dims);
  const counts = coords.map((c) => c.length - 1);

  const byLabel = new Map<string, { keys: Set<number>; cells: number[][] }>();
  forEachIndex(counts, (idx) => {
    const lo = idx.map((v, d) => coords[d][v]);
    const hi = idx.map((v, d) => coords[d][v + 1]);
    const winner = winnerAt(rules, lo, hi);
    if (winner === null) return;
    let entry = byLabel.get(winner.label);
    if (!entry) {
      entry = { keys: new Set<number>(), cells: [] };
      byLabel.set(winner.label, entry);
    }
    entry.keys.add(cellKey(idx, counts));
    entry.cells.push(idx);
  });

  const regions: Region[] = [];
  for (const label of [...byLabel.keys()].sort()) {
    const entry = byLabel.get(label)!;
    entry.cells.sort(compareIdx);
    for (const box of growBoxes(entry.cells, entry.keys, counts, coords)) {
      regions.push({ box, label });
    }
  }
  regions.sort((a, b) => compareIdx(a.box.lo, b.box.lo) || compareIdx(a.box.hi, b.box.hi));
  return regions;
}

/** 点查询:返回覆盖该点的区域标签,无覆盖返回 null。 */
export function queryPoint(regions: Region[], point: number[]): string | null {
  for (const region of regions) {
    const { lo, hi } = region.box;
    let inside = lo.length === point.length;
    for (let d = 0; inside && d < lo.length; d++) {
      if (point[d] < lo[d] || point[d] >= hi[d]) inside = false;
    }
    if (inside) return region.label;
  }
  return null;
}

/**
 * 核验分区:
 * - 区域两两不重叠;
 * - 在每个基本单元上,分区给出的标签与规则优先级决胜结果一致;
 * - 区域体积之和等于被覆盖基本单元的体积之和。
 */
export function verify(rules: Rule[], regions: Region[]): VerifyReport {
  const errors: string[] = [];
  const note = (msg: string): void => {
    if (errors.length < 20) errors.push(msg);
  };

  const dims =
    rules.length > 0 ? rules[0].box.lo.length : regions.length > 0 ? regions[0].box.lo.length : 0;
  if ((dims < 1 || dims > 3) && (rules.length > 0 || regions.length > 0)) {
    note(`only 1..3 dimensions are supported, got ${dims}`);
  }
  rules.forEach((r, i) => {
    try {
      checkBox(r.box, dims, `rule[${i}]`);
    } catch (e) {
      note((e as Error).message);
    }
  });
  regions.forEach((r, i) => {
    try {
      checkBox(r.box, dims, `region[${i}]`);
    } catch (e) {
      note((e as Error).message);
    }
  });

  let disjoint = true;
  for (let i = 0; i < regions.length; i++) {
    for (let j = i + 1; j < regions.length; j++) {
      if (boxesOverlap(regions[i].box, regions[j].box)) {
        disjoint = false;
        note(`region[${i}] overlaps region[${j}]`);
      }
    }
  }

  let semanticsMatch = true;
  let coveredVolume = 0;
  if (dims >= 1 && dims <= 3 && rules.length > 0) {
    for (let i = 0; i < regions.length; i++) {
      const winner = winnerAtPoint(rules, regions[i].box.lo);
      const expected = winner === null ? null : winner.label;
      if (expected !== regions[i].label) {
        semanticsMatch = false;
        note(
          `region[${i}] labeled ${regions[i].label} but rules give ${
            expected === null ? "null" : expected
          } at [${regions[i].box.lo}]`,
        );
      }
    }
    const coords = buildCoords(rules, dims);
    const counts = coords.map((c) => c.length - 1);
    forEachIndex(counts, (idx) => {
      const lo = idx.map((v, d) => coords[d][v]);
      const hi = idx.map((v, d) => coords[d][v + 1]);
      const winner = winnerAt(rules, lo, hi);
      const expected = winner === null ? null : winner.label;
      const actual = queryPoint(regions, lo);
      if (expected !== actual) {
        semanticsMatch = false;
        note(
          `cell [${lo}] -> [${hi}): expected ${expected === null ? "null" : expected}, got ${
            actual === null ? "null" : actual
          }`,
        );
      }
      if (expected !== null) coveredVolume += boxVolume({ lo, hi });
    });
  }

  const regionVolume = regions.reduce((sum, r) => sum + boxVolume(r.box), 0);
  if (regionVolume !== coveredVolume) {
    note(`volume mismatch: sum(regions)=${regionVolume}, covered=${coveredVolume}`);
  }

  const ok = errors.length === 0 && disjoint && semanticsMatch && regionVolume === coveredVolume;
  return { ok, disjoint, semanticsMatch, regionVolume, coveredVolume, errors };
}
