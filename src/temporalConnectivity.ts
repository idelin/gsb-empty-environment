/**
 * 时间区间连通性检查。
 *
 * 模型：每条边在半开区间 [start, end) 内生效。查询问的是：
 * source 与 target 是否在整个查询区间 [start, end) 内“始终”连通。
 *
 * 方法：取查询区间与所有边端点的并集切成原子时间片，
 * 每个时间片内生效边集恒定，对每片独立做一次 BFS 核对，
 * 而不是只测试区间两端。首个失败片即首次断开的最小时间片，
 * 并给出割证据 (S, T) 与跨越割的边及其失效原因。
 */

export interface TemporalEdge {
  readonly from: string;
  readonly to: string;
  /** 生效起点（含） */
  readonly start: number;
  /** 生效终点（不含） */
  readonly end: number;
}

export interface ConnectivityQuery {
  readonly source: string;
  readonly target: string;
  /** 查询起点（含） */
  readonly start: number;
  /** 查询终点（不含） */
  readonly end: number;
}

export interface CheckOptions {
  /** 默认 false，按无向边处理；true 时按 from -> to 有向处理 */
  readonly directed?: boolean;
}

export interface TimeSlice {
  readonly start: number;
  readonly end: number;
  /** 该片内生效的边（下标指向输入 edges 数组） */
  readonly activeEdgeIndices: readonly number[];
}

export type CutEdgeReason = "expired" | "not-yet-active";

export interface CutEdge {
  readonly edgeIndex: number;
  readonly edge: TemporalEdge;
  /** 该片内该边不生效的原因：已过期 / 尚未生效 */
  readonly reason: CutEdgeReason;
}

export interface SliceFailure {
  /** 首次断开的最小时间片 [sliceStart, sliceEnd) */
  readonly sliceStart: number;
  readonly sliceEnd: number;
  /** 割的 S 侧：片内从 source 可达的节点 */
  readonly reachable: readonly string[];
  /** 割的 T 侧：不可达节点（含 target） */
  readonly unreachable: readonly string[];
  /** 跨越割 (S, T) 的全部时序边；它们在该片内均不生效，构成割证据 */
  readonly cutEdges: readonly CutEdge[];
}

export interface ConnectivityResult {
  /** 整个查询区间内是否始终连通 */
  readonly connected: boolean;
  /** 切分出的全部原子时间片（每片都已独立 BFS 核对） */
  readonly slices: readonly TimeSlice[];
  /** 首次断开信息；始终连通或空查询区间时不存在 */
  readonly firstFailure?: SliceFailure;
}

/** 边在时刻 t 是否生效（半开区间） */
export function isActiveAt(edge: TemporalEdge, t: number): boolean {
  return edge.start <= t && t < edge.end;
}

/** 切分时间片：查询端点 + 落在查询区间内部的所有边端点 */
export function buildSlices(
  edges: readonly TemporalEdge[],
  query: ConnectivityQuery,
): readonly TimeSlice[] {
  if (query.start >= query.end) return [];

  const points = new Set<number>([query.start, query.end]);
  for (const edge of edges) {
    if (edge.start >= edge.end) continue; // 空生效窗，永不生效
    if (edge.start > query.start && edge.start < query.end) points.add(edge.start);
    if (edge.end > query.start && edge.end < query.end) points.add(edge.end);
  }
  const sorted = [...points].sort((a, b) => a - b);

  const slices: TimeSlice[] = [];
  for (let i = 0; i + 1 < sorted.length; i++) {
    const start = sorted[i]!;
    const end = sorted[i + 1]!;
    const activeEdgeIndices: number[] = [];
    for (let e = 0; e < edges.length; e++) {
      const edge = edges[e]!;
      if (edge.start >= edge.end) continue;
      // 片内无端点，左端点生效即整片生效
      if (isActiveAt(edge, start)) activeEdgeIndices.push(e);
    }
    slices.push({ start, end, activeEdgeIndices });
  }
  return slices;
}

function reachableFrom(
  source: string,
  edges: readonly TemporalEdge[],
  activeEdgeIndices: readonly number[],
  directed: boolean,
): Set<string> {
  const adjacency = new Map<string, string[]>();
  const addArc = (a: string, b: string) => {
    const list = adjacency.get(a);
    if (list) list.push(b);
    else adjacency.set(a, [b]);
  };
  for (const index of activeEdgeIndices) {
    const edge = edges[index]!;
    addArc(edge.from, edge.to);
    if (!directed) addArc(edge.to, edge.from);
  }

  const seen = new Set<string>([source]);
  const queue: string[] = [source];
  for (let head = 0; head < queue.length; head++) {
    const node = queue[head]!;
    for (const next of adjacency.get(node) ?? []) {
      if (!seen.has(next)) {
        seen.add(next);
        queue.push(next);
      }
    }
  }
  return seen;
}

function buildFailure(
  edges: readonly TemporalEdge[],
  query: ConnectivityQuery,
  slice: TimeSlice,
  reachable: Set<string>,
): SliceFailure {
  const nodes = new Set<string>([query.source, query.target]);
  for (const edge of edges) {
    nodes.add(edge.from);
    nodes.add(edge.to);
  }

  const unreachable = [...nodes].filter((n) => !reachable.has(n)).sort();
  const cutEdges: CutEdge[] = [];
  for (let e = 0; e < edges.length; e++) {
    const edge = edges[e]!;
    const crosses =
      (reachable.has(edge.from) && !reachable.has(edge.to)) ||
      (reachable.has(edge.to) && !reachable.has(edge.from));
    if (!crosses) continue;
    // 跨越割的边在该片内必然不生效，否则 BFS 已经越过割
    const reason: CutEdgeReason =
      edge.end <= slice.start ? "expired" : "not-yet-active";
    cutEdges.push({ edgeIndex: e, edge, reason });
  }

  return {
    sliceStart: slice.start,
    sliceEnd: slice.end,
    reachable: [...reachable].sort(),
    unreachable,
    cutEdges,
  };
}

/**
 * 判断 source 与 target 是否在整个查询区间 [start, end) 内始终连通。
 *
 * - 空查询区间（start >= end）： vacuously 连通，slices 为空。
 * - source === target：平凡连通。
 * - 逐片独立 BFS，首个失败片即首次断开的最小时间片。
 */
export function checkConnectivity(
  edges: readonly TemporalEdge[],
  query: ConnectivityQuery,
  options: CheckOptions = {},
): ConnectivityResult {
  const directed = options.directed ?? false;
  const slices = buildSlices(edges, query);

  for (const slice of slices) {
    const reachable = reachableFrom(query.source, edges, slice.activeEdgeIndices, directed);
    if (!reachable.has(query.target)) {
      return {
        connected: false,
        slices,
        firstFailure: buildFailure(edges, query, slice, reachable),
      };
    }
  }
  return { connected: true, slices };
}
