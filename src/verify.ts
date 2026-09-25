/**
 * 对照验证：在有限时间格 × 有限属性域上全枚举，
 * 用朴素逐规则求值（不做任何段编译）与引擎查询结果逐点比对。
 */

import { evalCond } from './conditions.ts';
import type { Attrs } from './conditions.ts';
import { PolicyEngine } from './engine.ts';
import type { Effect, Rule } from './engine.ts';

export interface VerifyOptions {
  timeMin: number;
  timeMax: number; // 不含
  domains: Readonly<Record<string, readonly number[]>>;
}

export interface Mismatch {
  time: number;
  attrs: Record<string, number>;
  expected: { effect: Effect; source: string | null };
  actual: { effect: Effect; source: string | null };
}

export interface VerifyReport {
  checked: number;
  mismatches: Mismatch[];
}

/** 朴素语义：覆盖 t 的规则按 (priority desc, version desc, id asc)，首个条件命中者生效，否则默认。 */
function naive(engine: PolicyEngine, rules: readonly Rule[], t: number, attrs: Attrs) {
  const covering = rules.filter((r) => r.start <= t && t < r.end);
  const ordered = covering.sort((a, b) =>
    b.priority - a.priority || b.version - a.version || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  for (const r of ordered) {
    if (evalCond(r.condition, attrs)) return { effect: r.effect, source: r.id as string | null };
  }
  return { effect: engine.defaultEffect, source: null as string | null };
}

export function verifyAgainstEnumeration(engine: PolicyEngine, opts: VerifyOptions): VerifyReport {
  const rules = engine.snapshotRules();
  const attrNames = Object.keys(opts.domains).sort();
  const mismatches: Mismatch[] = [];
  let checked = 0;

  const combos: Record<string, number>[] = [];
  const walk = (i: number, acc: Record<string, number>) => {
    if (i === attrNames.length) { combos.push({ ...acc }); return; }
    const name = attrNames[i];
    for (const v of opts.domains[name]) { acc[name] = v; walk(i + 1, acc); }
  };
  walk(0, {});
  if (combos.length === 0) combos.push({});

  for (let t = opts.timeMin; t < opts.timeMax; t++) {
    for (const attrs of combos) {
      const expect = naive(engine, rules, t, attrs);
      const got = engine.query(t, attrs);
      checked++;
      if (got.effect !== expect.effect || got.source !== expect.source) {
        mismatches.push({
          time: t,
          attrs,
          expected: expect,
          actual: { effect: got.effect, source: got.source },
        });
      }
    }
  }
  return { checked, mismatches };
}
