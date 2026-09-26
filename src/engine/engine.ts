// Incremental style engine.
//
// - Matches selectors per node and records exactly which nodes' attributes
//   each match consulted (class / id / state changes only re-match affected
//   nodes).
// - Tracks value dependencies between computed entries (node|prop), including
//   variable reads and inheritance, in a reverse dependency graph.
// - A mutation computes the dirty closure, evaluates only those entries, and
//   publishes the result as one atomic snapshot swap: readers holding an old
//   snapshot never observe a mixed-version style table.

import { parseStylesheet } from '../css/parser.ts';
import type { Rule } from '../css/parser.ts';
import { matchSelector } from '../css/selector.ts';
import { parseValueTokens } from '../css/values.ts';
import { flatten } from './model.ts';
import type { DocNode } from './model.ts';
import {
  compareDeclarations,
  isCustomProperty,
  makeKey,
  REGULAR_PROPERTIES,
  splitKey,
} from './computed.ts';
import type { ComputedValue, Declaration } from './computed.ts';
import { Evaluator } from './evaluator.ts';

export interface StyleSnapshot {
  readonly version: number;
  readonly store: ReadonlyMap<string, ComputedValue>;
  get(nodeId: number, prop: string): ComputedValue | undefined;
}

export interface UpdateReport {
  version: number;
  evaluated: string[]; // entry keys recomputed by this update
}

const REGULAR_PROPS = Object.keys(REGULAR_PROPERTIES);

export class StyleEngine {
  readonly nodes: DocNode[];
  private readonly nodeById = new Map<number, DocNode>();
  private rules: Rule[];
  private readonly inline = new Map<number, Map<string, string>>();
  private candidates = new Map<number, Declaration[]>();
  private nodeConsulted = new Map<number, Set<number>>(); // node -> nodes consulted by its matches
  private consultedBy = new Map<number, Set<number>>(); // node -> nodes whose matches consulted it
  private forwardDeps = new Map<string, Set<string>>();
  private reverseDeps = new Map<string, Set<string>>();
  private knownCustom = new Set<string>();
  private store = new Map<string, ComputedValue>();
  private version = 0;

  constructor(root: DocNode, css: string) {
    this.nodes = flatten(root);
    for (const n of this.nodes) this.nodeById.set(n.id, n);
    this.rules = parseStylesheet(css);
    this.refreshKnownCustom();
    for (const n of this.nodes) this.rebuildCandidates(n);
    const all: string[] = [];
    for (const n of this.nodes) {
      for (const prop of this.allProps()) all.push(makeKey(n.id, prop));
    }
    this.commit(new Set(all));
  }

  getSnapshot(): StyleSnapshot {
    const store = this.store;
    const version = this.version;
    return {
      version,
      store,
      get(nodeId: number, prop: string): ComputedValue | undefined {
        return store.get(makeKey(nodeId, prop));
      },
    };
  }

  setClass(nodeId: number, name: string, on: boolean): UpdateReport {
    const node = this.mustNode(nodeId);
    const changed = on ? !node.classes.has(name) : node.classes.has(name);
    if (changed) {
      if (on) node.classes.add(name);
      else node.classes.delete(name);
    }
    return this.afterAttributeChange(nodeId);
  }

  toggleState(nodeId: number, state: string): UpdateReport {
    const node = this.mustNode(nodeId);
    if (node.states.has(state)) node.states.delete(state);
    else node.states.add(state);
    return this.afterAttributeChange(nodeId);
  }

  setInline(nodeId: number, prop: string, rawValue: string | null): UpdateReport {
    this.mustNode(nodeId);
    const previousCustom = new Set(this.knownCustom);
    let map = this.inline.get(nodeId);
    if (!map) {
      map = new Map();
      this.inline.set(nodeId, map);
    }
    if (rawValue === null) map.delete(prop);
    else map.set(prop, rawValue);
    this.refreshKnownCustom();
    const dirty = new Set<string>();
    const changedProps = this.rebuildCandidates(this.nodeById.get(nodeId)!);
    for (const p of changedProps) dirty.add(makeKey(nodeId, p));
    dirty.add(makeKey(nodeId, prop));
    this.syncCustomVars(previousCustom, dirty);
    return this.commit(dirty);
  }

  updateStylesheet(css: string): UpdateReport {
    const previousCustom = new Set(this.knownCustom);
    this.rules = parseStylesheet(css);
    this.refreshKnownCustom();
    const dirty = new Set<string>();
    for (const n of this.nodes) {
      const changedProps = this.rebuildCandidates(n);
      for (const p of changedProps) dirty.add(makeKey(n.id, p));
    }
    this.syncCustomVars(previousCustom, dirty);
    return this.commit(dirty);
  }

  // Reconciles the entry universe with the current set of known custom
  // properties: newly known variables get entries on every node (dirtied
  // here); removed variables have their entries dropped immediately and their
  // dependents dirtied, so they recompute against the now-unset variable
  // instead of reading its stale committed value.
  private syncCustomVars(previousCustom: Set<string>, dirty: Set<string>): void {
    for (const name of this.knownCustom) {
      if (!previousCustom.has(name)) {
        for (const n of this.nodes) dirty.add(makeKey(n.id, name)); // newly known variable
      }
    }
    for (const name of previousCustom) {
      if (!this.knownCustom.has(name)) {
        for (const n of this.nodes) {
          const key = makeKey(n.id, name);
          if (this.store.has(key)) {
            for (const dependent of this.reverseDeps.get(key) ?? []) dirty.add(dependent);
            this.dropEntry(key);
            // A removed entry may itself be a dependent of another removed
            // entry (custom properties inherit); never re-evaluate it.
            dirty.delete(key);
          }
        }
      }
    }
  }

  private allProps(): string[] {
    return [...REGULAR_PROPS, ...this.knownCustom];
  }

  private mustNode(nodeId: number): DocNode {
    const node = this.nodeById.get(nodeId);
    if (!node) throw new Error(`unknown node ${nodeId}`);
    return node;
  }

  private refreshKnownCustom(): void {
    this.knownCustom = new Set();
    for (const rule of this.rules) {
      for (const d of rule.declarations) {
        if (isCustomProperty(d.prop)) this.knownCustom.add(d.prop);
      }
    }
    for (const map of this.inline.values()) {
      for (const prop of map.keys()) {
        if (isCustomProperty(prop)) this.knownCustom.add(prop);
      }
    }
  }

  private afterAttributeChange(nodeId: number): UpdateReport {
    const affected = new Set<number>(this.consultedBy.get(nodeId) ?? []);
    affected.add(nodeId);
    const dirty = new Set<string>();
    for (const id of affected) {
      const changedProps = this.rebuildCandidates(this.nodeById.get(id)!);
      for (const p of changedProps) dirty.add(makeKey(id, p));
    }
    return this.commit(dirty);
  }

  // Re-matches every selector entry against the node, rebuilds its candidate
  // declarations and consulted-edge bookkeeping, and returns the set of
  // properties whose candidate list actually changed.
  private rebuildCandidates(node: DocNode): Set<string> {
    const decls: Declaration[] = [];
    const consulted = new Set<number>();
    for (const rule of this.rules) {
      for (const entry of rule.selectors) {
        const touched: number[] = [];
        if (matchSelector(node, entry.selector, touched)) {
          for (const t of touched) consulted.add(t);
          for (const d of rule.declarations) {
            decls.push({
              ruleId: rule.id,
              inline: false,
              selectorText: entry.text,
              specificity: entry.specificity,
              prop: d.prop,
              raw: d.raw,
              tokens: d.tokens,
            });
          }
        } else {
          for (const t of touched) consulted.add(t);
        }
      }
    }
    const inlineMap = this.inline.get(node.id);
    if (inlineMap) {
      for (const [prop, raw] of inlineMap) {
        decls.push({
          ruleId: -1,
          inline: true,
          selectorText: 'inline',
          specificity: [0, 0, 0],
          prop,
          raw,
          tokens: parseValueTokens(raw),
        });
      }
    }
    const previous = this.candidates.get(node.id) ?? [];
    this.candidates.set(node.id, decls);
    this.replaceConsultedEdges(node.id, consulted);
    return diffCandidateProps(previous, decls);
  }

  private replaceConsultedEdges(nodeId: number, consulted: Set<number>): void {
    const old = this.nodeConsulted.get(nodeId);
    if (old) {
      for (const target of old) {
        const set = this.consultedBy.get(target);
        if (set) {
          set.delete(nodeId);
          if (set.size === 0) this.consultedBy.delete(target);
        }
      }
    }
    this.nodeConsulted.set(nodeId, consulted);
    for (const target of consulted) {
      let set = this.consultedBy.get(target);
      if (!set) {
        set = new Set();
        this.consultedBy.set(target, set);
      }
      set.add(nodeId);
    }
  }

  private declarationsFor(nodeId: number, prop: string): Declaration[] {
    return (this.candidates.get(nodeId) ?? []).filter((d) => d.prop === prop).sort(compareDeclarations);
  }

  private dropEntry(key: string): void {
    this.store.delete(key);
    const targets = this.forwardDeps.get(key) ?? new Set<string>();
    for (const t of targets) this.reverseDeps.get(t)?.delete(key);
    this.forwardDeps.delete(key);
    this.reverseDeps.delete(key);
  }

  private commit(dirtyRoots: Set<string>): UpdateReport {
    if (dirtyRoots.size === 0) return { version: this.version, evaluated: [] };
    // Only known entries (regular properties + known custom properties) may
    // enter the store; stale keys for removed variables are dropped here.
    const isKnownKey = (key: string): boolean => {
      const prop = splitKey(key)[1];
      return prop in REGULAR_PROPERTIES || this.knownCustom.has(prop);
    };
    // Expand the dirty set through the reverse dependency graph.
    const closure = new Set<string>();
    const queue = [...dirtyRoots].filter(isKnownKey);
    while (queue.length > 0) {
      const key = queue.pop()!;
      if (closure.has(key) || !isKnownKey(key)) continue;
      closure.add(key);
      for (const dependent of this.reverseDeps.get(key) ?? []) {
        if (!closure.has(dependent)) queue.push(dependent);
      }
    }
    const store = this.store;
    const evaluator = new Evaluator({
      parentOf: (nodeId) => this.nodeById.get(nodeId)?.parent?.id ?? null,
      declarationsFor: (nodeId, prop) => this.declarationsFor(nodeId, prop),
      committedGet: (key) => store.get(key),
      isClosureKey: (key) => closure.has(key),
    });
    for (const key of closure) evaluator.evaluate(key);
    // Build the next store off to the side, then swap it in atomically.
    const next = new Map(store);
    for (const key of closure) {
      const value = evaluator.memo.get(key);
      if (value) next.set(key, value);
    }
    // Rewire dependency edges for the recomputed entries.
    for (const key of closure) {
      for (const target of this.forwardDeps.get(key) ?? []) {
        const set = this.reverseDeps.get(target);
        if (set) {
          set.delete(key);
          if (set.size === 0) this.reverseDeps.delete(target);
        }
      }
      const deps = evaluator.deps.get(key) ?? new Set<string>();
      this.forwardDeps.set(key, deps);
      for (const target of deps) {
        let set = this.reverseDeps.get(target);
        if (!set) {
          set = new Set();
          this.reverseDeps.set(target, set);
        }
        set.add(key);
      }
    }
    this.store = next; // atomic publish: no mixed-version styles are visible
    this.version++;
    return { version: this.version, evaluated: [...closure] };
  }
}

function candidateSignature(decls: Declaration[]): Map<string, string> {
  const byProp = new Map<string, string[]>();
  for (const d of decls) {
    let list = byProp.get(d.prop);
    if (!list) {
      list = [];
      byProp.set(d.prop, list);
    }
    list.push(`${d.inline ? 'i' : 'r'}${d.ruleId}:${d.specificity.join(',')}:${d.raw}`);
  }
  const out = new Map<string, string>();
  for (const [prop, list] of byProp) out.set(prop, list.sort().join('|'));
  return out;
}

function diffCandidateProps(a: Declaration[], b: Declaration[]): Set<string> {
  const sa = candidateSignature(a);
  const sb = candidateSignature(b);
  const changed = new Set<string>();
  for (const [prop, sig] of sa) {
    if (sb.get(prop) !== sig) changed.add(prop);
  }
  for (const [prop, sig] of sb) {
    if (sa.get(prop) !== sig) changed.add(prop);
  }
  return changed;
}
