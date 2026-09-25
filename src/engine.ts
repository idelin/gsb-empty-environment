/**
 * 整数时间区间规则引擎。
 *
 * 把规则编译为不重叠的半开判定段 [start, end)。每段持有一条按优先级排序的
 * 决策链（条件按需惰性求值），段与段之间共享相同的决策链对象——
 * 不做 时间 × 属性 的笛卡尔展开。
 *
 * 支持增量插入/删除、批量原子提交；每次更新记录实际触及的段与决策节点。
 */

import { CondIntern, evalCond } from './conditions.ts';
import type { Attrs, Cond } from './conditions.ts';

export type Effect = 'allow' | 'deny';

export interface Rule {
  id: string;
  start: number; // 含
  end: number;   // 不含（半开）
  priority: number; // 大者先判
  version: number;  // 有效版本；同 id 重新插入视为新版本替换
  effect: Effect;
  condition: Cond;
}

export type BatchOp =
  | { type: 'insert'; rule: Rule }   // 同 id 已存在 => 端点/内容变更，撤回旧证据后重建
  | { type: 'delete'; id: string };

export interface ChainEntry {
  ruleId: string;
  version: number;
  effect: Effect;
  condId: number;
}

/** 决策链：一段内的共享决策结构。entries 按 (priority desc, version desc, id asc) 排序。 */
export interface DecisionChain {
  id: number;
  entries: readonly ChainEntry[];
}

export interface Segment {
  start: number;
  end: number;
  chain: DecisionChain;
}

export interface QueryResult {
  time: number;
  effect: Effect;
  /** 决定结果的来源规则；默认策略命中时为 null */
  source: string | null;
  /** 命中段 [start, end)；无段覆盖（空引擎）时为 null */
  segment: readonly [number, number] | null;
  chainId: number | null;
  /** 该段内全部候选规则（证据），按判定顺序 */
  evidence: readonly string[];
}

export interface TouchedSegment {
  start: number;
  end: number;
  chainId: number;
}

export interface BatchReport {
  applied: number;
  /** 本次实际触及（重建）的段 */
  touchedSegments: TouchedSegment[];
  /** 本次实际触及的决策节点 id */
  touchedDecisionNodes: number[];
  /** 被撤回旧证据的规则 id（删除或端点/内容变更） */
  retracted: string[];
  inserted: string[];
}

function validateRule(rule: Rule): void {
  if (!rule.id) throw new Error('规则缺少 id');
  if (!Number.isInteger(rule.start) || !Number.isInteger(rule.end))
    throw new Error(`规则 ${rule.id} 端点必须是整数`);
  if (!(rule.start < rule.end))
    throw new Error(`规则 ${rule.id} 需要 start < end，得到 [${rule.start}, ${rule.end})`);
  if (!Number.isInteger(rule.priority)) throw new Error(`规则 ${rule.id} priority 必须是整数`);
  if (!Number.isInteger(rule.version) || rule.version < 1)
    throw new Error(`规则 ${rule.id} version 必须是 >= 1 的整数`);
  if (rule.effect !== 'allow' && rule.effect !== 'deny')
    throw new Error(`规则 ${rule.id} effect 非法: ${rule.effect}`);
}

export class PolicyEngine {
  readonly defaultEffect: Effect;
  private rules = new Map<string, Rule>();
  private segments: Segment[] = [];
  private conds = new CondIntern();
  private chainsByKey = new Map<string, DecisionChain>();
  private nextChainId = 0;

  constructor(defaultEffect: Effect = 'deny') {
    this.defaultEffect = defaultEffect;
  }

  snapshotRules(): readonly Rule[] {
    return [...this.rules.values()];
  }

  getSegments(): readonly Segment[] {
    return this.segments;
  }

  get condPoolSize(): number {
    return this.conds.size;
  }

  get chainPoolSize(): number {
    return this.chainsByKey.size;
  }

  private internChain(entries: ChainEntry[]): DecisionChain {
    const key = entries.map((e) => `${e.ruleId}@${e.version}:${e.effect}#${e.condId}`).join('|');
    const hit = this.chainsByKey.get(key);
    if (hit) return hit;
    const chain: DecisionChain = { id: this.nextChainId++, entries };
    this.chainsByKey.set(key, chain);
    return chain;
  }

  /** 段内候选规则排序：优先级降序、版本降序、id 升序（确定性）。 */
  private static orderRules(rules: Rule[]): Rule[] {
    return rules.sort((a, b) =>
      b.priority - a.priority || b.version - a.version || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  }

  private chainFor(active: Rule[]): DecisionChain {
    const ordered = PolicyEngine.orderRules(active);
    const entries: ChainEntry[] = ordered.map((r) => ({
      ruleId: r.id,
      version: r.version,
      effect: r.effect,
      condId: this.conds.idOf(r.condition),
    }));
    return this.internChain(entries);
  }

  /**
   * 批量原子更新：先完整构建新状态，全部成功才一次性提交；任一 op 非法则整体拒绝。
   * 只重建受影响时间范围内的段，范围外的段与决策链对象原样保留（结构共享）。
   */
  applyBatch(ops: readonly BatchOp[]): BatchReport {
    if (ops.length === 0)
      return { applied: 0, touchedSegments: [], touchedDecisionNodes: [], retracted: [], inserted: [] };

    // 1) 全量校验，任何失败都不改状态
    for (const op of ops) {
      if (op.type === 'insert') validateRule(op.rule);
      else if (op.type === 'delete') {
        if (!this.rules.has(op.id)) throw new Error(`删除失败：规则 ${op.id} 不存在`);
      } else throw new Error(`未知操作: ${JSON.stringify(op)}`);
    }

    // 2) 在副本上应用
    const next = new Map(this.rules);
    const retracted: string[] = [];
    const inserted: string[] = [];
    let dirtyLo = Infinity;
    let dirtyHi = -Infinity;
    const markDirty = (s: number, e: number) => {
      dirtyLo = Math.min(dirtyLo, s);
      dirtyHi = Math.max(dirtyHi, e);
    };
    for (const op of ops) {
      if (op.type === 'insert') {
        const old = next.get(op.rule.id);
        if (old) { markDirty(old.start, old.end); retracted.push(old.id); } // 端点改变 => 撤回旧证据
        next.set(op.rule.id, op.rule);
        markDirty(op.rule.start, op.rule.end);
        inserted.push(op.rule.id);
      } else {
        const old = next.get(op.id)!;
        next.delete(op.id);
        markDirty(old.start, old.end);
        retracted.push(old.id);
      }
    }

    // 3) 重建脏区间内的段
    const bounds = new Set<number>([dirtyLo, dirtyHi]);
    for (const r of next.values()) {
      if (r.start < dirtyHi && r.end > dirtyLo) {
        bounds.add(Math.max(r.start, dirtyLo));
        bounds.add(Math.min(r.end, dirtyHi));
      }
    }
    const cuts = [...bounds].sort((a, b) => a - b);
    const rebuilt: Segment[] = [];
    const active = [...next.values()].filter((r) => r.start < dirtyHi && r.end > dirtyLo);
    for (let i = 0; i + 1 < cuts.length; i++) {
      const s = cuts[i], e = cuts[i + 1];
      const covering = active.filter((r) => r.start <= s && r.end >= e);
      if (covering.length === 0) continue; // 无规则覆盖 => 默认策略，不留段
      const chain = this.chainFor(covering);
      const last = rebuilt[rebuilt.length - 1];
      if (last && last.end === s && last.chain === chain) last.end = e; // 同源相邻段合并
      else rebuilt.push({ start: s, end: e, chain });
    }

    // 4) 与未触及部分拼接（保持对象共享）：跨界段裁剪后保留原决策链对象
    const kept: Segment[] = [];
    for (const sg of this.segments) {
      if (sg.end <= dirtyLo || sg.start >= dirtyHi) { kept.push(sg); continue; }
      if (sg.start < dirtyLo) kept.push({ start: sg.start, end: dirtyLo, chain: sg.chain });
      if (sg.end > dirtyHi) kept.push({ start: dirtyHi, end: sg.end, chain: sg.chain });
    }
    const merged: Segment[] = [];
    for (const sg of [...kept, ...rebuilt].sort((a, b) => a.start - b.start)) {
      const last = merged[merged.length - 1];
      if (last && last.end === sg.start && last.chain === sg.chain) last.end = sg.end;
      else merged.push(sg);
    }

    // 5) 原子提交
    this.rules = next;
    this.segments = merged;

    const touchedDecisionNodes = [...new Set(rebuilt.map((s) => s.chain.id))];
    return {
      applied: ops.length,
      touchedSegments: rebuilt.map((s) => ({ start: s.start, end: s.end, chainId: s.chain.id })),
      touchedDecisionNodes,
      retracted,
      inserted,
    };
  }

  insert(rule: Rule): BatchReport {
    return this.applyBatch([{ type: 'insert', rule }]);
  }

  delete(id: string): BatchReport {
    return this.applyBatch([{ type: 'delete', id }]);
  }

  /** 查询：定位段后沿决策链惰性求值，首个条件命中的规则决定结果与来源。 */
  query(time: number, attrs: Attrs = {}): QueryResult {
    if (!Number.isInteger(time)) throw new Error(`查询时间必须是整数，得到 ${time}`);
    let lo = 0, hi = this.segments.length - 1, hit: Segment | null = null;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const sg = this.segments[mid];
      if (time < sg.start) hi = mid - 1;
      else if (time >= sg.end) lo = mid + 1;
      else { hit = sg; break; }
    }
    if (!hit) {
      return { time, effect: this.defaultEffect, source: null, segment: null, chainId: null, evidence: [] };
    }
    for (const entry of hit.chain.entries) {
      if (evalCond(this.conds.node(entry.condId), attrs)) {
        return {
          time,
          effect: entry.effect,
          source: entry.ruleId,
          segment: [hit.start, hit.end],
          chainId: hit.chain.id,
          evidence: hit.chain.entries.map((e) => e.ruleId),
        };
      }
    }
    return {
      time,
      effect: this.defaultEffect,
      source: null,
      segment: [hit.start, hit.end],
      chainId: hit.chain.id,
      evidence: hit.chain.entries.map((e) => e.ruleId),
    };
  }
}
