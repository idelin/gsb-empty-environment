/**
 * Interval policy engine.
 *
 * Compiles integer half-open time-interval rules into a list of
 * non-overlapping decision segments. Every segment references a shared,
 * immutable DecisionNode (canonicalized by its rule set), so segments
 * with identical rule sets share one decision structure instead of
 * expanding time x attributes into a Cartesian table.
 *
 * Supports incremental insert / delete / upsert and atomic batches.
 * Every update reports the segments and decision nodes actually touched,
 * plus added / retracted evidence ranges.
 */

import { evalCond, validateCond, type Attrs, type Cond, type Schema } from './conditions.ts';

export type Decision = 'allow' | 'deny';

export interface Rule {
  readonly id: string;
  readonly version: number;
  /** Inclusive integer start of the half-open interval. */
  readonly start: number;
  /** Exclusive integer end of the half-open interval. */
  readonly end: number;
  readonly decision: Decision;
  /** Higher priority wins when several rules match. */
  readonly priority: number;
  readonly condition: Cond;
}

export interface DecisionNode {
  readonly id: string;
  readonly key: string;
  /** Rules sorted by priority desc, version desc, id asc. */
  readonly rules: readonly Rule[];
}

export interface Segment {
  readonly start: number;
  readonly end: number;
  readonly node: DecisionNode;
}

export interface Evidence {
  readonly ruleId: string;
  readonly version: number;
  readonly start: number;
  readonly end: number;
}

export interface UpdateReport {
  readonly ops: number;
  /** Segment ranges (post-merge) whose decision structure changed. */
  readonly touchedSegments: readonly { readonly start: number; readonly end: number }[];
  /** Decision node ids that were created or referenced by changed segments. */
  readonly touchedNodeIds: readonly string[];
  /** Evidence ranges added by this update. */
  readonly added: readonly Evidence[];
  /** Evidence ranges withdrawn by this update (e.g. endpoint moves, deletes). */
  readonly retracted: readonly Evidence[];
}

export interface QueryResult {
  readonly time: number;
  readonly decision: Decision;
  /** Source rule evidence; null when the default policy applied. */
  readonly source: { readonly ruleId: string; readonly version: number; readonly priority: number } | null;
  readonly nodeId: string | null;
  readonly segment: { readonly start: number; readonly end: number } | null;
  readonly defaultApplied: boolean;
}

export type BatchOp =
  | { readonly type: 'insert'; readonly rule: Rule }
  | { readonly type: 'upsert'; readonly rule: Rule }
  | { readonly type: 'remove'; readonly ruleId: string };

export interface EngineOptions {
  readonly schema: Schema;
  readonly defaultDecision: Decision;
}

function ruleOrder(a: Rule, b: Rule): number {
  if (a.priority !== b.priority) return b.priority - a.priority;
  if (a.version !== b.version) return b.version - a.version;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

function nodeKey(rules: readonly Rule[]): string {
  return rules.map((r) => `${r.id}@${r.version}`).join('|');
}

class ReportBuilder {
  private readonly touchedSegs = new Map<string, { start: number; end: number }>();
  private readonly touchedNodes = new Set<string>();
  private readonly addedEv: Evidence[] = [];
  private readonly retractedEv: Evidence[] = [];
  private opCount = 0;

  bumpOps(): void { this.opCount++; }
  touch(start: number, end: number, nodeId: string): void {
    this.touchedSegs.set(`${start}:${end}`, { start, end });
    this.touchedNodes.add(nodeId);
  }
  addEvidence(ev: Evidence): void { this.addedEv.push(ev); }
  retractEvidence(ev: Evidence): void { this.retractedEv.push(ev); }

  build(): UpdateReport {
    const segs = [...this.touchedSegs.values()].sort((a, b) => a.start - b.start);
    return {
      ops: this.opCount,
      touchedSegments: segs,
      touchedNodeIds: [...this.touchedNodes].sort(),
      added: this.addedEv,
      retracted: this.retractedEv,
    };
  }
}

export class PolicyEngine {
  private readonly schema: Schema;
  private readonly defaultDecision: Decision;
  private rulesById = new Map<string, Rule>();
  private segments: Segment[] = [];
  private readonly nodeCache = new Map<string, DecisionNode>();
  private nodeSeq = 0;

  constructor(options: EngineOptions) {
    this.schema = options.schema;
    this.defaultDecision = options.defaultDecision;
  }

  // ---------------------------------------------------------------- queries

  query(time: number, attrs: Attrs = {}): QueryResult {
    if (!Number.isInteger(time)) throw new Error(`query time must be an integer, got ${time}`);
    const seg = this.findSegment(time);
    if (!seg) {
      return { time, decision: this.defaultDecision, source: null, nodeId: null, segment: null, defaultApplied: true };
    }
    for (const rule of seg.node.rules) {
      if (evalCond(rule.condition, attrs)) {
        return {
          time,
          decision: rule.decision,
          source: { ruleId: rule.id, version: rule.version, priority: rule.priority },
          nodeId: seg.node.id,
          segment: { start: seg.start, end: seg.end },
          defaultApplied: false,
        };
      }
    }
    return {
      time,
      decision: this.defaultDecision,
      source: null,
      nodeId: seg.node.id,
      segment: { start: seg.start, end: seg.end },
      defaultApplied: true,
    };
  }

  /** Snapshot of current non-overlapping segments (for inspection/tests). */
  getSegments(): readonly Segment[] {
    return this.segments;
  }

  getRule(id: string): Rule | undefined {
    return this.rulesById.get(id);
  }

  listRules(): readonly Rule[] {
    return [...this.rulesById.values()];
  }

  private findSegment(time: number): Segment | undefined {
    let lo = 0;
    let hi = this.segments.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const seg = this.segments[mid]!;
      if (time < seg.start) hi = mid - 1;
      else if (time >= seg.end) lo = mid + 1;
      else return seg;
    }
    return undefined;
  }

  // ---------------------------------------------------------------- updates

  insert(rule: Rule): UpdateReport {
    return this.applyBatch([{ type: 'insert', rule }]);
  }

  remove(ruleId: string): UpdateReport {
    return this.applyBatch([{ type: 'remove', ruleId }]);
  }

  upsert(rule: Rule): UpdateReport {
    return this.applyBatch([{ type: 'upsert', rule }]);
  }

  /**
   * Atomic batch: every op is validated up front; the state is swapped in
   * only if all ops succeed, so results and sources update atomically.
   */
  applyBatch(ops: readonly BatchOp[]): UpdateReport {
    if (ops.length === 0) return new ReportBuilder().build();
    this.validateBatch(ops);

    const savedRules = this.rulesById;
    const savedSegments = this.segments;
    const report = new ReportBuilder();
    try {
      for (const op of ops) {
        report.bumpOps();
        switch (op.type) {
          case 'insert':
          case 'upsert': {
            const existing = this.rulesById.get(op.rule.id);
            if (existing) this.applyRemove(existing, report);
            this.applyInsert(op.rule, report);
            break;
          }
          case 'remove': {
            const existing = this.rulesById.get(op.ruleId)!;
            this.applyRemove(existing, report);
            break;
          }
        }
      }
    } catch (err) {
      this.rulesById = savedRules;
      this.segments = savedSegments;
      throw err;
    }
    return report.build();
  }

  // ------------------------------------------------------------ validation

  private validateBatch(ops: readonly BatchOp[]): void {
    const planned = new Map(this.rulesById);
    ops.forEach((op, i) => {
      const at = `op[${i}]`;
      if (op.type === 'remove') {
        if (!planned.has(op.ruleId)) throw new Error(`${at}: cannot remove unknown rule "${op.ruleId}"`);
        planned.delete(op.ruleId);
        return;
      }
      const rule = op.rule;
      this.validateRule(rule, at);
      const existing = planned.get(rule.id);
      if (op.type === 'insert' && existing) {
        throw new Error(`${at}: rule "${rule.id}" already exists (use upsert)`);
      }
      if (existing && rule.version < existing.version) {
        throw new Error(
          `${at}: stale version ${rule.version} for rule "${rule.id}" (effective version is ${existing.version})`,
        );
      }
      planned.set(rule.id, rule);
    });
  }

  private validateRule(rule: Rule, at: string): void {
    if (!rule.id) throw new Error(`${at}: rule id must be non-empty`);
    if (!Number.isInteger(rule.version) || rule.version < 1) {
      throw new Error(`${at}: rule "${rule.id}" version must be a positive integer`);
    }
    if (!Number.isInteger(rule.start) || !Number.isInteger(rule.end)) {
      throw new Error(`${at}: rule "${rule.id}" endpoints must be integers`);
    }
    if (rule.start >= rule.end) {
      throw new Error(`${at}: rule "${rule.id}" requires start < end (half-open [start, end))`);
    }
    const condErrors = validateCond(rule.condition, this.schema, `${at}.condition`);
    if (condErrors.length > 0) throw new Error(condErrors.join('; '));
  }

  // ------------------------------------------------------- internal mutate

  private canonicalNode(rules: readonly Rule[]): DecisionNode {
    const sorted = [...rules].sort(ruleOrder);
    const key = nodeKey(sorted);
    let node = this.nodeCache.get(key);
    if (!node) {
      node = { id: `N${++this.nodeSeq}`, key, rules: sorted };
      this.nodeCache.set(key, node);
    }
    return node;
  }

  private applyInsert(rule: Rule, report: ReportBuilder): void {
    const next: Segment[] = [];
    for (const seg of this.segments) {
      if (seg.end <= rule.start || seg.start >= rule.end) {
        next.push(seg);
        continue;
      }
      if (seg.start < rule.start) next.push({ start: seg.start, end: rule.start, node: seg.node });
      const midStart = Math.max(seg.start, rule.start);
      const midEnd = Math.min(seg.end, rule.end);
      const node = this.canonicalNode([...seg.node.rules, rule]);
      next.push({ start: midStart, end: midEnd, node });
      report.touch(midStart, midEnd, node.id);
      if (seg.end > rule.end) next.push({ start: rule.end, end: seg.end, node: seg.node });
    }
    // Fill gaps inside [start, end) that no existing segment covered.
    const gaps: Segment[] = [];
    const covered: Array<{ start: number; end: number }> = this.segments
      .filter((s) => s.end > rule.start && s.start < rule.end)
      .map((s) => ({ start: Math.max(s.start, rule.start), end: Math.min(s.end, rule.end) }))
      .sort((a, b) => a.start - b.start);
    let pos = rule.start;
    const gapNode = this.canonicalNode([rule]);
    for (const c of covered) {
      if (c.start > pos) gaps.push({ start: pos, end: c.start, node: gapNode });
      pos = Math.max(pos, c.end);
    }
    if (pos < rule.end) gaps.push({ start: pos, end: rule.end, node: gapNode });
    for (const g of gaps) report.touch(g.start, g.end, gapNode.id);
    next.push(...gaps);

    this.segments = mergeAdjacent(next);
    this.rulesById.set(rule.id, rule);
    report.addEvidence({ ruleId: rule.id, version: rule.version, start: rule.start, end: rule.end });
  }

  private applyRemove(rule: Rule, report: ReportBuilder): void {
    const next: Segment[] = [];
    for (const seg of this.segments) {
      if (seg.end <= rule.start || seg.start >= rule.end) {
        next.push(seg);
        continue;
      }
      if (seg.start < rule.start) next.push({ start: seg.start, end: rule.start, node: seg.node });
      const remaining = seg.node.rules.filter((r) => r.id !== rule.id);
      if (remaining.length > 0) {
        const node = this.canonicalNode(remaining);
        const midStart = Math.max(seg.start, rule.start);
        const midEnd = Math.min(seg.end, rule.end);
        next.push({ start: midStart, end: midEnd, node });
        report.touch(midStart, midEnd, node.id);
      } else {
        report.touch(Math.max(seg.start, rule.start), Math.min(seg.end, rule.end), seg.node.id);
      }
      if (seg.end > rule.end) next.push({ start: rule.end, end: seg.end, node: seg.node });
    }
    this.segments = mergeAdjacent(next);
    this.rulesById.delete(rule.id);
    report.retractEvidence({ ruleId: rule.id, version: rule.version, start: rule.start, end: rule.end });
  }
}

/** Merge adjacent segments that share the same decision-node key. */
function mergeAdjacent(segments: readonly Segment[]): Segment[] {
  const sorted = [...segments].sort((a, b) => a.start - b.start);
  const out: Segment[] = [];
  for (const seg of sorted) {
    const last = out[out.length - 1];
    if (last && last.end === seg.start && last.node.key === seg.node.key) {
      out[out.length - 1] = { start: last.start, end: seg.end, node: last.node };
    } else {
      out.push(seg);
    }
  }
  return out;
}
