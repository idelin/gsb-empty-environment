// Batch evaluator: computes a closure of dirty entries with memoization and
// cycle detection. Entries outside the closure are read from the committed
// store, so an update only recomputes what the dependency graph marks dirty.

import type { ValueToken } from '../css/values.ts';
import {
  buildChain,
  declarationSource,
  isCustomProperty,
  makeKey,
  REGULAR_PROPERTIES,
  splitKey,
} from './computed.ts';
import type { ComputedValue, Declaration, VarResolution } from './computed.ts';

export interface EvalEnv {
  parentOf(nodeId: number): number | null;
  declarationsFor(nodeId: number, prop: string): Declaration[]; // sorted in cascade order
  committedGet(key: string): ComputedValue | undefined;
  isClosureKey(key: string): boolean;
}

type SubstResult = { ok: true; text: string } | { ok: false; reason: 'undefined' | 'cycle' | 'invalid'; cyclePath?: string[] };

export class Evaluator {
  readonly memo = new Map<string, ComputedValue>();
  readonly deps = new Map<string, Set<string>>(); // entry -> entries it read
  private readonly stack: string[] = [];
  private readonly cyclic = new Set<string>();
  private readonly cyclicPaths = new Map<string, string[]>();
  private readonly env: EvalEnv;

  constructor(env: EvalEnv) {
    this.env = env;
  }

  evaluate(key: string): ComputedValue {
    const cached = this.memo.get(key);
    if (cached) return cached;
    const stackHit = this.stack.indexOf(key);
    if (stackHit >= 0) {
      // Cyclic custom-property dependency: everything on the stack from the
      // first occurrence is invalid at computed-value time (guaranteed-invalid).
      // The path is sorted so it is independent of evaluation order.
      const cyclePath = this.stack.slice(stackHit).map((k) => splitKey(k)[1]).sort();
      for (let j = stackHit; j < this.stack.length; j++) {
        this.cyclic.add(this.stack[j]);
        this.cyclicPaths.set(this.stack[j], cyclePath);
      }
      return {
        prop: splitKey(key)[1],
        value: null,
        source: { kind: 'invalid', unsetTo: 'initial' },
        chain: [],
        vars: [],
        invalidReason: 'cycle',
        cyclePath,
      };
    }
    if (!this.env.isClosureKey(key)) {
      const committed = this.env.committedGet(key);
      if (committed) return committed;
      // Unset custom property (never declared anywhere): guaranteed-invalid.
      return {
        prop: splitKey(key)[1],
        value: null,
        source: { kind: 'initial' },
        chain: [],
        vars: [],
        invalidReason: 'undefined',
      };
    }
    const [nodeId, prop] = splitKey(key);
    this.stack.push(key);
    let result = isCustomProperty(prop) ? this.evalCustom(nodeId, prop) : this.evalRegular(nodeId, prop);
    this.stack.pop();
    if (this.cyclic.has(key)) {
      // This entry participates in a cycle: forced invalid regardless of the
      // value its own substitution produced (per css-variables cycle rules).
      result = {
        ...result,
        value: null,
        source: { kind: 'invalid', unsetTo: 'initial' },
        invalidReason: 'cycle',
        cyclePath: this.cyclicPaths.get(key),
      };
    }
    this.memo.set(key, result);
    return result;
  }

  private read(key: string): ComputedValue {
    const from = this.stack[this.stack.length - 1];
    if (from !== undefined) {
      let set = this.deps.get(from);
      if (!set) {
        set = new Set();
        this.deps.set(from, set);
      }
      set.add(key);
    }
    return this.evaluate(key);
  }

  private evalCustom(nodeId: number, prop: string): ComputedValue {
    const decls = this.env.declarationsFor(nodeId, prop);
    if (decls.length === 0) {
      const parent = this.env.parentOf(nodeId);
      if (parent !== null) {
        const inherited = this.read(makeKey(parent, prop));
        return {
          prop,
          value: inherited.value,
          source: { kind: 'inherited', from: parent },
          chain: [],
          vars: [],
          invalidReason: inherited.invalidReason,
          cyclePath: inherited.cyclePath,
        };
      }
      return { prop, value: null, source: { kind: 'initial' }, chain: [], vars: [], invalidReason: 'undefined' };
    }
    const winner = decls[decls.length - 1];
    const vars: VarResolution[] = [];
    const res = this.substitute(winner.tokens, nodeId, vars);
    if (res.ok) {
      return { prop, value: res.text.trim(), source: declarationSource(winner), chain: buildChain(decls, false), vars };
    }
    return {
      prop,
      value: null,
      source: { kind: 'invalid', unsetTo: 'initial' },
      chain: buildChain(decls, true),
      vars,
      invalidReason: res.reason,
      cyclePath: res.cyclePath,
    };
  }

  private evalRegular(nodeId: number, prop: string): ComputedValue {
    const meta = REGULAR_PROPERTIES[prop];
    const parent = this.env.parentOf(nodeId);
    const decls = this.env.declarationsFor(nodeId, prop);
    if (decls.length > 0) {
      const winner = decls[decls.length - 1];
      const vars: VarResolution[] = [];
      const res = this.substitute(winner.tokens, nodeId, vars);
      if (res.ok) {
        return { prop, value: res.text.trim(), source: declarationSource(winner), chain: buildChain(decls, false), vars };
      }
      // Invalid at computed-value time: the winning declaration is dropped and
      // the property behaves as `unset` (inherit if inherited, else initial).
      const chain = buildChain(decls, true);
      if (meta.inherited && parent !== null) {
        const inherited = this.read(makeKey(parent, prop));
        return { prop, value: inherited.value, source: { kind: 'invalid', unsetTo: 'inherited', from: parent }, chain, vars };
      }
      return { prop, value: meta.initial, source: { kind: 'invalid', unsetTo: 'initial' }, chain, vars };
    }
    if (meta.inherited && parent !== null) {
      const inherited = this.read(makeKey(parent, prop));
      return { prop, value: inherited.value, source: { kind: 'inherited', from: parent }, chain: [], vars: [] };
    }
    return { prop, value: meta.initial, source: { kind: 'initial' }, chain: [], vars: [] };
  }

  private substitute(tokens: ValueToken[], nodeId: number, vars: VarResolution[]): SubstResult {
    let text = '';
    for (const token of tokens) {
      if (token.kind === 'text') {
        text += token.text;
        continue;
      }
      const entry: VarResolution = { name: token.name, status: 'invalid' };
      vars.push(entry);
      const target = this.read(makeKey(nodeId, token.name));
      if (target.value !== null) {
        entry.status = 'resolved';
        entry.value = target.value;
        text += target.value;
        continue;
      }
      // The referenced variable is invalid: record why (undefined / cycle /
      // invalid substitution) so the fallback path keeps its failure condition.
      entry.reason = target.invalidReason ?? 'undefined';
      if (target.cyclePath) entry.cyclePath = target.cyclePath;
      if (token.fallback) {
        const sub = this.substitute(token.fallback, nodeId, vars);
        if (sub.ok) {
          entry.status = 'fallback';
          entry.value = sub.text.trim();
          text += sub.text;
          continue;
        }
        return { ok: false, reason: sub.reason, cyclePath: sub.cyclePath };
      }
      return { ok: false, reason: entry.reason, cyclePath: entry.cyclePath };
    }
    return { ok: true, text };
  }
}
