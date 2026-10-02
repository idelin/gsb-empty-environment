/**
 * 形式化约束巡检与不变式断言仪。
 *
 * 输入：由反射解码产物构建的实体图（EntityGraph）。
 * 巡检项：
 *   1. 悬挂引用（dangling reference）—— 指向不存在实体的边；
 *   2. 孤立环路（orphan cycle）—— 从任何根不可达的强连通环；
 *   3. 数据倾斜（data skew）—— 入度分布的热点与 Gini 系数；
 *   4. 不变式断言（invariants）—— 用户注册的全量状态谓词，逐一求值。
 * 引擎为离线全量巡检：inspect() 返回完整报告，assertClean() 在任一违规时抛出。
 * 所有检查均为纯函数式遍历，不修改图，可并发安全地对同一图运行。
 */

import { InvariantViolationError } from './errors.ts';
import type { DecodedMessage, MessageDescriptor } from './schema.ts';

export interface EntityRef {
  field: string;
  targetType: string;
  targetId: string;
}

export interface Entity {
  key: string;
  type: string;
  id: string;
  refs: EntityRef[];
}

export function entityKey(type: string, id: string | number | bigint): string {
  return `${type}#${String(id)}`;
}

export class EntityGraph {
  readonly entities = new Map<string, Entity>();
  readonly duplicateIds: string[] = [];
  private readonly roots = new Set<string>();

  addEntity(type: string, id: string | number | bigint, refs: EntityRef[] = []): Entity {
    const key = entityKey(type, id);
    const existing = this.entities.get(key);
    if (existing) {
      this.duplicateIds.push(key);
      return existing;
    }
    const entity: Entity = { key, type, id: String(id), refs: [...refs] };
    this.entities.set(key, entity);
    return entity;
  }

  addRoot(type: string, id: string | number | bigint): void {
    this.roots.add(entityKey(type, id));
  }

  get rootKeys(): ReadonlySet<string> {
    return this.roots;
  }

  /** 从反射解码产物递归采集实体：id 字段作主键，ref 字段作引用边。 */
  collect(desc: MessageDescriptor, msg: DecodedMessage): Entity | null {
    let idValue: unknown;
    const refs: EntityRef[] = [];
    for (const field of desc.fields) {
      const value = msg[field.name];
      if (value === undefined || value === null) continue;
      if (field.id) idValue = value;
      if (field.ref) {
        const values = field.repeated ? (value as unknown[]) : [value];
        for (const v of values) {
          refs.push({ field: field.name, targetType: field.ref, targetId: String(v) });
        }
      }
      if (field.kind === 'message' && field.message) {
        const children = field.repeated ? (value as DecodedMessage[]) : [value as DecodedMessage];
        for (const child of children) this.collect(field.message, child);
      }
    }
    if (idValue === undefined || idValue === null) return null;
    return this.addEntity(desc.typeName, idValue as string | number | bigint, refs);
  }
}

/* ---------------- 巡检项 ---------------- */

export interface DanglingRef {
  from: string;
  field: string;
  targetType: string;
  targetId: string;
}

export function findDanglingReferences(graph: EntityGraph): DanglingRef[] {
  const out: DanglingRef[] = [];
  for (const entity of graph.entities.values()) {
    for (const ref of entity.refs) {
      if (!graph.entities.has(entityKey(ref.targetType, ref.targetId))) {
        out.push({ from: entity.key, field: ref.field, targetType: ref.targetType, targetId: ref.targetId });
      }
    }
  }
  return out;
}

export interface OrphanCycle {
  members: string[];
}

/** 仅保留指向已存在实体的边（悬挂边另行报告）。 */
function buildAdjacency(graph: EntityGraph): Map<string, string[]> {
  const adj = new Map<string, string[]>();
  for (const entity of graph.entities.values()) {
    const targets: string[] = [];
    for (const ref of entity.refs) {
      const key = entityKey(ref.targetType, ref.targetId);
      if (graph.entities.has(key)) targets.push(key);
    }
    adj.set(entity.key, targets);
  }
  return adj;
}

function reachableFrom(roots: Iterable<string>, adj: Map<string, string[]>): Set<string> {
  const seen = new Set<string>();
  const stack = [...roots];
  while (stack.length > 0) {
    const key = stack.pop() as string;
    if (seen.has(key) || !adj.has(key)) continue;
    seen.add(key);
    for (const next of adj.get(key) as string[]) stack.push(next);
  }
  return seen;
}

/** 迭代式 Tarjan SCC，避免深图递归爆栈。 */
function stronglyConnectedComponents(adj: Map<string, string[]>): string[][] {
  const index = new Map<string, number>();
  const lowlink = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const sccs: string[][] = [];
  let counter = 0;

  for (const root of adj.keys()) {
    if (index.has(root)) continue;
    const work: [string, number][] = [[root, 0]];
    while (work.length > 0) {
      const frame = work[work.length - 1];
      const [node, childIdx] = frame;
      if (childIdx === 0) {
        index.set(node, counter);
        lowlink.set(node, counter);
        counter++;
        stack.push(node);
        onStack.add(node);
      }
      const children = adj.get(node) as string[];
      if (childIdx < children.length) {
        frame[1] = childIdx + 1;
        const child = children[childIdx];
        if (!index.has(child)) {
          work.push([child, 0]);
        } else if (onStack.has(child)) {
          lowlink.set(node, Math.min(lowlink.get(node) as number, index.get(child) as number));
        }
      } else {
        work.pop();
        if (work.length > 0) {
          const parent = work[work.length - 1][0];
          lowlink.set(parent, Math.min(lowlink.get(parent) as number, lowlink.get(node) as number));
        }
        if (lowlink.get(node) === index.get(node)) {
          const scc: string[] = [];
          let member: string;
          do {
            member = stack.pop() as string;
            onStack.delete(member);
            scc.push(member);
          } while (member !== node);
          sccs.push(scc);
        }
      }
    }
  }
  return sccs;
}

export function findOrphanCycles(graph: EntityGraph): OrphanCycle[] {
  const adj = buildAdjacency(graph);
  let roots = [...graph.rootKeys];
  if (roots.length === 0) {
    const hasIncoming = new Set<string>();
    for (const targets of adj.values()) for (const t of targets) hasIncoming.add(t);
    roots = [...adj.keys()].filter((k) => !hasIncoming.has(k));
  }
  const reachable = reachableFrom(roots, adj);

  const orphans: OrphanCycle[] = [];
  for (const scc of stronglyConnectedComponents(adj)) {
    const isSelfLoop = scc.length === 1 && (adj.get(scc[0]) as string[]).includes(scc[0]);
    const isCycle = scc.length > 1 || isSelfLoop;
    if (isCycle && scc.every((member) => !reachable.has(member))) {
      orphans.push({ members: scc.sort() });
    }
  }
  return orphans;
}

export interface SkewReport {
  entityCount: number;
  edgeCount: number;
  maxInDegree: number;
  meanInDegree: number;
  maxToMeanRatio: number;
  gini: number;
  hotKeys: string[];
}

export function analyzeSkew(graph: EntityGraph, hotFactor = 3): SkewReport {
  const inDegree = new Map<string, number>();
  for (const key of graph.entities.keys()) inDegree.set(key, 0);
  let edgeCount = 0;
  for (const entity of graph.entities.values()) {
    for (const ref of entity.refs) {
      const key = entityKey(ref.targetType, ref.targetId);
      if (inDegree.has(key)) {
        inDegree.set(key, (inDegree.get(key) as number) + 1);
        edgeCount++;
      }
    }
  }
  const degrees = [...inDegree.values()];
  const n = degrees.length;
  const total = degrees.reduce((a, b) => a + b, 0);
  const mean = n === 0 ? 0 : total / n;
  const max = degrees.reduce((a, b) => Math.max(a, b), 0);

  const sorted = [...degrees].sort((a, b) => a - b);
  let weighted = 0;
  for (let i = 0; i < n; i++) weighted += (i + 1) * sorted[i];
  const gini = n === 0 || total === 0 ? 0 : (2 * weighted) / (n * total) - (n + 1) / n;

  const threshold = Math.max(2, mean * hotFactor);
  const hotKeys = [...inDegree.entries()]
    .filter(([, d]) => d >= threshold)
    .map(([k]) => k)
    .sort();

  return {
    entityCount: n,
    edgeCount,
    maxInDegree: max,
    meanInDegree: mean,
    maxToMeanRatio: mean === 0 ? 0 : max / mean,
    gini,
    hotKeys,
  };
}

/* ---------------- 不变式断言 ---------------- */

export interface InvariantViolation {
  invariant: string;
  entity?: string;
  detail: string;
}

export type InvariantFn = (graph: EntityGraph) => InvariantViolation[] | void;

export interface InspectionReport {
  danglingRefs: DanglingRef[];
  orphanCycles: OrphanCycle[];
  skew: SkewReport;
  invariantViolations: InvariantViolation[];
  stats: { entityCount: number; edgeCount: number; duplicateIds: string[] };
}

export interface InspectOptions {
  skewHotFactor?: number;
}

export class IntegrityInspector {
  private readonly invariants: { name: string; fn: InvariantFn }[] = [];
  private readonly graph: EntityGraph;
  private readonly options: InspectOptions;

  constructor(graph: EntityGraph, options: InspectOptions = {}) {
    this.graph = graph;
    this.options = options;
  }

  addInvariant(name: string, fn: InvariantFn): this {
    this.invariants.push({ name, fn });
    return this;
  }

  inspect(): InspectionReport {
    const invariantViolations: InvariantViolation[] = [];
    for (const key of this.graph.duplicateIds) {
      invariantViolations.push({ invariant: 'unique-entity-id', entity: key, detail: `duplicate entity id ${key}` });
    }
    for (const { name, fn } of this.invariants) {
      const found = fn(this.graph) ?? [];
      for (const v of found) invariantViolations.push({ invariant: name, ...v });
    }
    const skew = analyzeSkew(this.graph, this.options.skewHotFactor ?? 3);
    return {
      danglingRefs: findDanglingReferences(this.graph),
      orphanCycles: findOrphanCycles(this.graph),
      skew,
      invariantViolations,
      stats: {
        entityCount: skew.entityCount,
        edgeCount: skew.edgeCount,
        duplicateIds: [...this.graph.duplicateIds],
      },
    };
  }

  isClean(report: InspectionReport): boolean {
    return (
      report.danglingRefs.length === 0 &&
      report.orphanCycles.length === 0 &&
      report.invariantViolations.length === 0 &&
      report.skew.hotKeys.length === 0
    );
  }

  assertClean(): InspectionReport {
    const report = this.inspect();
    if (!this.isClean(report)) {
      const parts: string[] = [];
      if (report.danglingRefs.length) parts.push(`${report.danglingRefs.length} dangling ref(s)`);
      if (report.orphanCycles.length) parts.push(`${report.orphanCycles.length} orphan cycle(s)`);
      if (report.skew.hotKeys.length) parts.push(`${report.skew.hotKeys.length} skewed hot key(s)`);
      if (report.invariantViolations.length) parts.push(`${report.invariantViolations.length} invariant violation(s)`);
      throw new InvariantViolationError(`integrity inspection failed: ${parts.join(', ')}`, report);
    }
    return report;
  }
}
