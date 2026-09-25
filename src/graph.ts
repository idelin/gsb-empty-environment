/**
 * 迁移版本图。
 *
 * 节点是版本（字符串）；有向边是一次迁移步骤，带 priority（数字越小越优先）。
 * - 确定性选路：在 from->to 的所有简单路径中选“路径成本最小”的一条。
 *   路径成本 = 各边 priority 之和（平局不做任何隐式 tie-break，直接拒绝）。
 * - 两条同优先路径 => AMBIGUOUS_PATH，迁移被拒绝（不会执行任何步骤）。
 */
import type { JsonObject, JsonValue } from './json.ts';
import type { Schema } from './schema.ts';
import { MigrationError } from './errors.ts';

export interface MigrationStep {
  /** 步骤标识（同一条边内唯一），用于错误定位与断点日志。 */
  name: string;
  /** 输入 schema：执行前校验当前数据。 */
  input: Schema;
  /** 输出 schema：执行后校验新数据（契约保证）。 */
  output: Schema;
  apply: (data: JsonObject, ctx: MigrationContext) => JsonObject;
}

export interface MigrationContext {
  edgeId: string;
  stepName: string;
  versionPath: string[];
}

export interface MigrationEdge {
  id: string;
  from: string;
  to: string;
  priority: number;
  steps: MigrationStep[];
}

export interface MigrationGraph {
  edges: MigrationEdge[];
  /** 邻接表（构建时按 id 排序，保证遍历确定）。 */
  adjacency: Map<string, MigrationEdge[]>;
  /** id -> edge 索引。 */
  byId: Map<string, MigrationEdge>;
}

function isJsonValue(v: unknown): v is JsonValue {
  if (v === null) return true;
  const t = typeof v;
  if (t === 'string' || t === 'boolean' || t === 'number') return Number.isFinite(v as number) || t !== 'number';
  if (Array.isArray(v)) return v.every(isJsonValue);
  if (t === 'object') return Object.values(v as Record<string, unknown>).every(isJsonValue);
  return false;
}

function requireNonEmptyString(v: unknown, location: string, what: string): string {
  if (typeof v !== 'string' || v.length === 0) {
    throw new MigrationError({
      code: 'INVALID_GRAPH',
      message: `${what} must be a non-empty string`,
      location,
    });
  }
  return v;
}

export function buildGraph(edges: Array<Omit<MigrationEdge, 'id'> & { id?: string }>): MigrationGraph {
  if (!Array.isArray(edges) || edges.length === 0) {
    throw new MigrationError({
      code: 'INVALID_GRAPH',
      message: 'graph must contain at least one edge',
      location: '/edges',
    });
  }

  const normalized: MigrationEdge[] = edges.map((e, i) => {
    const from = requireNonEmptyString(e.from, `/edges[${i}]/from`, 'edge.from');
    const to = requireNonEmptyString(e.to, `/edges[${i}]/to`, 'edge.to');
    const id = e.id ?? `${from}->${to}`;
    requireNonEmptyString(id, `/edges[${i}]/id`, 'edge.id');

    if (typeof e.priority !== 'number' || !Number.isFinite(e.priority)) {
      throw new MigrationError({
        code: 'INVALID_GRAPH',
        message: `edge ${id}: priority must be a finite number`,
        location: `/edges[${i}]/priority`,
      });
    }
    if (!Array.isArray(e.steps) || e.steps.length === 0) {
      throw new MigrationError({
        code: 'INVALID_GRAPH',
        message: `edge ${id}: steps must be a non-empty array`,
        location: `/edges[${i}]/steps`,
      });
    }
    e.steps.forEach((step, j) => {
      requireNonEmptyString(step.name, `/edges[${i}]/steps[${j}]/name`, 'step.name');
      if (typeof step.apply !== 'function') {
        throw new MigrationError({
          code: 'INVALID_GRAPH',
          message: `edge ${id} step ${step.name}: apply must be a function`,
          location: `/edges[${i}]/steps[${j}]/apply`,
        });
      }
      if (step.input === undefined || step.output === undefined) {
        throw new MigrationError({
          code: 'INVALID_GRAPH',
          message: `edge ${id} step ${step.name}: input/output schema required`,
          location: `/edges[${i}]/steps[${j}]`,
        });
      }
    });
    const names = new Set(e.steps.map((s) => s.name));
    if (names.size !== e.steps.length) {
      throw new MigrationError({
        code: 'INVALID_GRAPH',
        message: `edge ${id}: duplicate step names`,
        location: `/edges[${i}]/steps`,
      });
    }
    if (!isJsonValue({ from, to, priority: e.priority, id })) {
      throw new MigrationError({
        code: 'INVALID_GRAPH',
        message: `edge ${id}: non-JSON-compatible descriptor`,
        location: `/edges[${i}]`,
      });
    }
    return { id, from, to, priority: e.priority, steps: e.steps };
  });

  const byId = new Map<string, MigrationEdge>();
  for (const edge of normalized) {
    if (byId.has(edge.id)) {
      throw new MigrationError({
        code: 'INVALID_GRAPH',
        message: `duplicate edge id: ${edge.id}`,
        location: `/edges`,
      });
    }
    byId.set(edge.id, edge);
  }

  const adjacency = new Map<string, MigrationEdge[]>();
  for (const edge of normalized) {
    const list = adjacency.get(edge.from) ?? [];
    list.push(edge);
    adjacency.set(edge.from, list);
  }
  for (const list of adjacency.values()) {
    list.sort((a, b) => a.id.localeCompare(b.id));
  }

  return { edges: normalized, adjacency, byId };
}

export interface PlannedPath {
  /** 选中的边（按执行顺序）。 */
  edges: MigrationEdge[];
  /** 版本序列，如 ["v1","v2","v4"]。 */
  versionPath: string[];
  /** 路径成本（priority 之和）。 */
  cost: number;
}

/**
 * 解析 from -> to 的确定性路径。
 * 成本最小且唯一才返回；同成本路径多于一条即抛 AMBIGUOUS_PATH。
 */
export function resolvePath(graph: MigrationGraph, from: string, to: string): PlannedPath {
  const found: Array<{ edges: MigrationEdge[]; cost: number }> = [];

  const dfs = (current: string, edges: MigrationEdge[], cost: number, seen: Set<string>): void => {
    if (current === to) {
      found.push({ edges: [...edges], cost });
      return;
    }
    for (const edge of graph.adjacency.get(current) ?? []) {
      if (seen.has(edge.to)) continue;
      seen.add(edge.to);
      edges.push(edge);
      dfs(edge.to, edges, cost + edge.priority, seen);
      edges.pop();
      seen.delete(edge.to);
    }
  };

  dfs(from, [], 0, new Set([from]));

  if (found.length === 0) {
    throw new MigrationError({
      code: 'NO_PATH',
      message: `no migration path from ${from} to ${to}`,
      versionPath: [from, to],
    });
  }

  found.sort((a, b) => a.cost - b.cost);
  const best = found[0] as { edges: MigrationEdge[]; cost: number };
  const tied = found.filter((p) => p.cost === best.cost);

  if (tied.length > 1) {
    const toVersionPath = (p: { edges: MigrationEdge[] }) => [from, ...p.edges.map((e) => e.to)];
    throw new MigrationError({
      code: 'AMBIGUOUS_PATH',
      message: `ambiguous migration from ${from} to ${to}: ${tied.length} paths share cost ${best.cost}`,
      versionPath: [from, to],
      candidates: tied.map(toVersionPath),
      tiedPaths: [toVersionPath(tied[0] as { edges: MigrationEdge[] }), toVersionPath(tied[1] as { edges: MigrationEdge[] })],
    });
  }

  return {
    edges: best.edges,
    cost: best.cost,
    versionPath: [from, ...best.edges.map((e) => e.to)],
  };
}

/**
 * 预检：对整张图做一次全对可达性歧义扫描。
 * 返回所有歧义（调用方可据此拒绝发布图配置）。
 */
export function detectAmbiguities(graph: MigrationGraph): Array<MigrationError> {
  const versions = new Set<string>();
  for (const edge of graph.edges) {
    versions.add(edge.from);
    versions.add(edge.to);
  }
  const errors: MigrationError[] = [];
  for (const a of versions) {
    for (const b of versions) {
      if (a === b) continue;
      try {
        resolvePath(graph, a, b);
      } catch (err) {
        if (err instanceof MigrationError && err.details.code === 'AMBIGUOUS_PATH') {
          errors.push(err);
        }
      }
    }
  }
  return errors;
}
