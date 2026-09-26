// Declarative specification: hierarchical modes, event table (guards + atomic
// multi-field actions), invariants, unreachable-state predicates, finite input domain.

export interface ModeNode {
  readonly name: string;
  readonly children?: readonly ModeNode[];
}

/** Does `pattern` match mode `path`? Segments split on '.'; `*` = one segment, `**` = any rest. */
export function modeMatches(pattern: string, path: string): boolean {
  const p = pattern.split('.');
  const s = path.split('.');
  let i = 0;
  let j = 0;
  while (i < p.length) {
    if (p[i] === '**') return true;
    if (j >= s.length) return false;
    if (p[i] !== '*' && p[i] !== s[j]) return false;
    i += 1;
    j += 1;
  }
  return j === s.length;
}

// Mode paths are relative to the root: root 'app' with child 'active' yields 'active'.
export function modeExists(root: ModeNode, path: string): boolean {
  const segments = path.split('.');
  let nodes: readonly ModeNode[] = root.children ?? [];
  for (const seg of segments) {
    const next = nodes.find((n) => n.name === seg);
    if (!next) return false;
    nodes = next.children ?? [];
  }
  return true;
}

export function listModes(root: ModeNode): string[] {
  const out: string[] = [];
  const walk = (node: ModeNode, prefix: string) => {
    for (const child of node.children ?? []) {
      const path = prefix === '' ? child.name : `${prefix}.${child.name}`;
      out.push(path);
      walk(child, path);
    }
  };
  walk(root, '');
  return out;
}

export interface Event<P = unknown> {
  readonly type: string;
  readonly payload?: P;
}

export interface EventDef<S> {
  readonly name: string;
  /** mode patterns; event is legal only when current mode matches one of them */
  readonly modes: readonly string[];
  /** returns null when passed, otherwise a human-readable rejection reason */
  readonly guard?: (state: S, event: Event) => string | null;
  /** atomic multi-field action: computes the whole next state or throws */
  readonly action: (state: S, event: Event) => S;
  /** finite payload domain used by the model checker */
  readonly domain?: () => readonly unknown[];
}

export interface Invariant<S> {
  readonly name: string;
  /** returns null when satisfied, otherwise a violation description */
  readonly check: (state: S) => string | null;
}

export interface UnreachablePredicate<S> {
  readonly name: string;
  /** true means "this state must be unreachable" */
  readonly matches: (state: S) => boolean;
}

export interface Spec<S> {
  readonly name: string;
  readonly modeRoot: ModeNode;
  readonly initial: () => S;
  readonly modeOf: (state: S) => string;
  readonly events: readonly EventDef<S>[];
  readonly invariants: readonly Invariant<S>[];
  readonly unreachable?: readonly UnreachablePredicate<S>[];
  /** returns a rejection reason when event.payload.target does not resolve in state */
  readonly resolveTarget?: (state: S, event: Event) => string | null;
}

/** Deterministic stable stringify (sorted keys) + FNV-1a hash. */
export function stableStringify(value: unknown): string {
  const seen = (v: unknown): unknown => {
    if (Array.isArray(v)) return v.map(seen);
    if (v !== null && typeof v === 'object') {
      const obj = v as Record<string, unknown>;
      return Object.fromEntries(Object.keys(obj).sort().map((k) => [k, seen(obj[k])]));
    }
    return v;
  };
  return JSON.stringify(seen(value));
}

export function stateHash(state: unknown): string {
  const s = stableStringify(state);
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, '0');
}
