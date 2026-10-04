// 区间资源分配器
//
// 语义约定:
// - 区间为半开区间 [start, end);start == end 是零长区间,视为空集,
//   不与任何区间(包括其他零长区间)冲突。
// - resource 字段为固定绑定,分配时必须保留;同一资源上的固定区间若
//   互相重叠,属于冲突,直接以冲突对的形式返回,不再尝试分配。
// - 资源编号是非负整数,固定绑定中的编号可以存在空洞;新分配的区间
//   优先复用已用编号,其次填充空洞,再向外扩展。
// - 目标:为未绑定的区间分配资源,使不同资源的总数最少。
//
// 注意:带固定绑定的区间分配等价于区间图上的预着色扩展问题
// (precoloring extension),它是 NP-hard 的;可行解所需的颜色数不能
// 仅由最大重叠数推断。因此短实例做精确搜索保证最优,长实例退化为
// 按起点排序的贪心着色。

export interface IntervalInput {
  id: string;
  start: number;
  end: number;
  resource?: number;
}

export interface ConflictPair {
  resource: number;
  a: string;
  b: string;
}

export interface ResourceSchedule {
  resource: number;
  intervals: string[];
}

export interface AllocationResult {
  ok: boolean;
  conflicts: ConflictPair[];
  assignments: Record<string, number>;
  schedule: ResourceSchedule[];
  resourceCount: number;
  exact: boolean;
}

export interface AllocateOptions {
  exactThreshold?: number;
}

const DEFAULT_EXACT_THRESHOLD: number = 24;

interface Item {
  id: string;
  start: number;
  end: number;
  pin: number;
}

interface Undo {
  v: number;
  c: number;
}

interface SweepEvent {
  pos: number;
  delta: number;
}

function isEmptyItem(it: Item): boolean {
  return it.end <= it.start;
}

function overlaps(a: Item, b: Item): boolean {
  if (isEmptyItem(a) || isEmptyItem(b)) return false;
  return a.start < b.end && b.start < a.end;
}

function maxPointOverlap(items: Item[]): number {
  const events: SweepEvent[] = [];
  for (const it of items) {
    if (isEmptyItem(it)) continue;
    events.push({ pos: it.start, delta: 1 });
    events.push({ pos: it.end, delta: -1 });
  }
  events.sort((a: SweepEvent, b: SweepEvent) => {
    if (a.pos !== b.pos) return a.pos - b.pos;
    return a.delta - b.delta;
  });
  let cur = 0;
  let best = 0;
  for (const ev of events) {
    cur += ev.delta;
    if (cur > best) best = cur;
  }
  return best;
}

function buildAdjacency(items: Item[]): number[][] {
  const n = items.length;
  const adj: number[][] = [];
  for (let i = 0; i < n; i++) adj.push([]);
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      if (overlaps(items[i], items[j])) {
        adj[i].push(j);
        adj[j].push(i);
      }
    }
  }
  return adj;
}

// 按起点排序的贪心着色:每个未绑定区间取未被重叠邻居占用的最小编号。
// 无固定绑定时对区间图是最优的;有固定绑定的情况下作为上界/兜底。
function greedyColor(items: Item[], adj: number[][]): number[] {
  const n = items.length;
  const colors: number[] = new Array(n).fill(-1);
  for (let i = 0; i < n; i++) {
    if (items[i].pin >= 0) colors[i] = items[i].pin;
  }
  const order: number[] = [];
  for (let i = 0; i < n; i++) order.push(i);
  order.sort((a: number, b: number) => {
    if (items[a].start !== items[b].start) return items[a].start - items[b].start;
    if (items[a].end !== items[b].end) return items[a].end - items[b].end;
    return a - b;
  });
  for (const v of order) {
    if (colors[v] >= 0) continue;
    const used = new Set<number>();
    for (const u of adj[v]) {
      if (colors[u] >= 0) used.add(colors[u]);
    }
    let c = 0;
    while (used.has(c)) c++;
    colors[v] = c;
  }
  return colors;
}

// 由固定编号加最小的空洞编号组成大小为 k 的调色板。
// 新颜色之间可互换,因此取最小可用编号不失一般性。
function buildPalette(pins: number[], k: number): number[] {
  const pinSet = new Set<number>();
  for (const p of pins) {
    if (p >= 0) pinSet.add(p);
  }
  const palette: number[] = [];
  for (const p of pinSet) palette.push(p);
  let x = 0;
  while (palette.length < k) {
    if (!pinSet.has(x)) palette.push(x);
    x++;
  }
  palette.sort((a: number, b: number) => a - b);
  return palette;
}

// 用给定调色板做精确 K 着色:MRV 选点 + 前向检查的回溯搜索。
function kColorable(items: Item[], adj: number[][], palette: number[]): number[] | null {
  const n = items.length;
  const k = palette.length;
  const paletteIndex = new Map<number, number>();
  for (let i = 0; i < k; i++) paletteIndex.set(palette[i], i);

  const colorIdx: number[] = new Array(n).fill(-1);
  const dom: boolean[][] = [];
  const domSize: number[] = [];
  for (let v = 0; v < n; v++) {
    const d: boolean[] = new Array(k).fill(true);
    if (items[v].pin >= 0) {
      const pi = paletteIndex.get(items[v].pin);
      if (pi === undefined) return null;
      for (let c = 0; c < k; c++) d[c] = c === pi;
    }
    dom.push(d);
    domSize.push(items[v].pin >= 0 ? 1 : k);
  }

  const undo: Undo[] = [];

  function canAssign(v: number, c: number): boolean {
    for (const u of adj[v]) {
      if (colorIdx[u] === c) return false;
    }
    return true;
  }

  function assign(v: number, c: number): boolean {
    colorIdx[v] = c;
    for (const u of adj[v]) {
      if (colorIdx[u] < 0 && dom[u][c]) {
        dom[u][c] = false;
        domSize[u]--;
        undo.push({ v: u, c: c });
        if (domSize[u] === 0) return false;
      }
    }
    return true;
  }

  function unassign(v: number, mark: number): void {
    colorIdx[v] = -1;
    while (undo.length > mark) {
      const top = undo.pop();
      if (top === undefined) break;
      dom[top.v][top.c] = true;
      domSize[top.v]++;
    }
  }

  for (let v = 0; v < n; v++) {
    if (items[v].pin < 0) continue;
    const pi = paletteIndex.get(items[v].pin);
    if (pi === undefined) return null;
    if (!canAssign(v, pi)) return null;
    if (!assign(v, pi)) return null;
  }

  function search(): boolean {
    let pick = -1;
    let best = k + 1;
    for (let v = 0; v < n; v++) {
      if (colorIdx[v] >= 0) continue;
      if (domSize[v] < best) {
        best = domSize[v];
        pick = v;
      }
    }
    if (pick < 0) return true;
    if (best === 0) return false;
    const mark = undo.length;
    for (let c = 0; c < k; c++) {
      if (!dom[pick][c]) continue;
      if (!canAssign(pick, c)) continue;
      const placed = assign(pick, c);
      if (placed && search()) return true;
      unassign(pick, mark);
    }
    return false;
  }

  if (!search()) return null;
  const colors: number[] = new Array(n);
  for (let v = 0; v < n; v++) colors[v] = palette[colorIdx[v]];
  return colors;
}

// 精确着色:颜色数从下界(最大重叠数与固定色数的较大者)开始递增,
// 第一个可行的 K 即为最优;贪心结果作为上界保证循环必然终止。
function exactColor(items: Item[], adj: number[][]): number[] {
  const pins: number[] = items.map((it: Item) => it.pin);
  const pinSet = new Set<number>();
  for (const p of pins) {
    if (p >= 0) pinSet.add(p);
  }
  const lb = Math.max(pinSet.size, maxPointOverlap(items));
  const greedy = greedyColor(items, adj);
  const ubSet = new Set<number>();
  for (const c of greedy) ubSet.add(c);
  const ub = ubSet.size;
  for (let k = lb; k <= ub; k++) {
    const palette = buildPalette(pins, k);
    const colors = kColorable(items, adj, palette);
    if (colors !== null) return colors;
  }
  return greedy;
}

export function allocateResources(intervals: IntervalInput[], options: AllocateOptions = {}): AllocationResult {
  const threshold: number = options.exactThreshold !== undefined ? options.exactThreshold : DEFAULT_EXACT_THRESHOLD;

  const items: Item[] = [];
  const seen = new Set<string>();
  for (const raw of intervals) {
    if (typeof raw.id !== 'string' || raw.id.length === 0) {
      throw new Error('interval id must be a non-empty string');
    }
    if (seen.has(raw.id)) {
      throw new Error('duplicate interval id: ' + raw.id);
    }
    seen.add(raw.id);
    if (!Number.isFinite(raw.start) || !Number.isFinite(raw.end)) {
      throw new Error('interval ' + raw.id + ': start/end must be finite numbers');
    }
    if (raw.end < raw.start) {
      throw new Error('interval ' + raw.id + ': end is before start');
    }
    let pin = -1;
    if (raw.resource !== undefined) {
      if (!Number.isInteger(raw.resource) || raw.resource < 0) {
        throw new Error('interval ' + raw.id + ': resource must be a non-negative integer');
      }
      pin = raw.resource;
    }
    items.push({ id: raw.id, start: raw.start, end: raw.end, pin: pin });
  }

  const adj = buildAdjacency(items);

  // 固定绑定冲突检测:同一资源上的固定区间两两检查重叠。
  const pinnedByResource = new Map<number, number[]>();
  for (let i = 0; i < items.length; i++) {
    if (items[i].pin < 0) continue;
    const list = pinnedByResource.get(items[i].pin);
    if (list === undefined) pinnedByResource.set(items[i].pin, [i]);
    else list.push(i);
  }
  const conflicts: ConflictPair[] = [];
  for (const [resource, members] of pinnedByResource) {
    for (let a = 0; a < members.length; a++) {
      for (let b = a + 1; b < members.length; b++) {
        if (overlaps(items[members[a]], items[members[b]])) {
          conflicts.push({
            resource: resource,
            a: items[members[a]].id,
            b: items[members[b]].id,
          });
        }
      }
    }
  }
  if (conflicts.length > 0) {
    return {
      ok: false,
      conflicts: conflicts,
      assignments: {},
      schedule: [],
      resourceCount: 0,
      exact: true,
    };
  }

  let freeCount = 0;
  for (const it of items) {
    if (it.pin < 0) freeCount++;
  }

  let colors: number[];
  let exact: boolean;
  if (freeCount <= threshold) {
    colors = exactColor(items, adj);
    exact = true;
  } else {
    colors = greedyColor(items, adj);
    exact = false;
  }

  const assignments: Record<string, number> = {};
  const groups = new Map<number, string[]>();
  for (let i = 0; i < items.length; i++) {
    assignments[items[i].id] = colors[i];
    const list = groups.get(colors[i]);
    if (list === undefined) groups.set(colors[i], [items[i].id]);
    else list.push(items[i].id);
  }

  const byId = new Map<string, Item>();
  for (const it of items) byId.set(it.id, it);

  const resourceNums: number[] = [];
  for (const key of groups.keys()) resourceNums.push(key);
  resourceNums.sort((a: number, b: number) => a - b);

  const schedule: ResourceSchedule[] = [];
  for (const r of resourceNums) {
    const ids = groups.get(r);
    if (ids === undefined) continue;
    ids.sort((x: string, y: string) => {
      const xi = byId.get(x);
      const yi = byId.get(y);
      if (xi === undefined || yi === undefined) return 0;
      if (xi.start !== yi.start) return xi.start - yi.start;
      if (xi.end !== yi.end) return xi.end - yi.end;
      return x < y ? -1 : 1;
    });
    schedule.push({ resource: r, intervals: ids });
  }

  return {
    ok: true,
    conflicts: [],
    assignments: assignments,
    schedule: schedule,
    resourceCount: resourceNums.length,
    exact: exact,
  };
}
