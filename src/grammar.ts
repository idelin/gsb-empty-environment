import { AstNode, SourcePos } from './ast';

export type AttrKind = 'synthesized' | 'inherited';

/** Relative address of another AST node, used to wire attribute dependencies. */
export type AttrTarget =
  | { readonly on: 'self' }
  | { readonly on: 'parent' }
  | { readonly on: 'child'; readonly index: number }
  | { readonly on: 'sibling'; readonly offset: number };

/** A reference to an attribute instance on a (relatively addressed) node. */
export type AttrRef = AttrTarget & { readonly attr: string };

export const self = (attr: string): AttrRef => ({ on: 'self', attr });
export const parent = (attr: string): AttrRef => ({ on: 'parent', attr });
export const child = (index: number, attr: string): AttrRef => ({ on: 'child', index, attr });
export const sibling = (offset: number, attr: string): AttrRef => ({ on: 'sibling', offset, attr });

/** A problem reported while evaluating a semantic rule. */
export interface Diagnostic {
  readonly message: string;
  readonly pos: SourcePos;
  readonly nodeId: number;
}

/** Services available to a semantic rule while it computes its attribute. */
export interface RuleContext {
  /** Read the (already evaluated) value of a referenced attribute instance. */
  get<T = unknown>(ref: AttrRef): T;
  /** Report a diagnostic anchored at `pos` (defaults to the current node). */
  report(message: string, pos?: SourcePos): void;
}

/**
 * A semantic rule: defines how one attribute of one node kind is computed,
 * and statically declares every attribute instance it reads.
 */
export interface SemanticRule {
  readonly attr: string;
  readonly kind: AttrKind;
  /** Attribute instances this rule reads, relative to the node being evaluated. */
  dependsOn(node: AstNode): AttrRef[];
  compute(node: AstNode, ctx: RuleContext): unknown;
}

/** An attribute grammar: node kind -> semantic rules. */
export class AttributeGrammar {
  private readonly rules = new Map<string, SemanticRule[]>();

  define(nodeKind: string, rule: SemanticRule): this {
    const list = this.rules.get(nodeKind) ?? [];
    if (list.some((r) => r.attr === rule.attr)) {
      throw new Error(`duplicate attribute '${rule.attr}' for node kind '${nodeKind}'`);
    }
    list.push(rule);
    this.rules.set(nodeKind, list);
    return this;
  }

  rulesFor(kind: string): readonly SemanticRule[] {
    return this.rules.get(kind) ?? [];
  }
}

/** Thrown when the attribute dependency graph contains a cycle. */
export class CycleError extends Error {
  constructor(readonly cycle: readonly string[]) {
    super(`circular attribute dependency detected: ${cycle.join(' -> ')}`);
    this.name = 'CycleError';
  }
}

/** Thrown when a rule references an attribute that no rule defines. */
export class UndefinedAttributeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UndefinedAttributeError';
  }
}

export interface EvaluationResult {
  /** Read the evaluated value of `attr` on `node`. Throws if undefined. */
  get<T = unknown>(node: AstNode, attr: string): T;
  has(node: AstNode, attr: string): boolean;
  readonly diagnostics: Diagnostic[];
  /** Attribute instance keys in the order they were evaluated (topological). */
  readonly evaluationOrder: readonly string[];
}

interface Instance {
  readonly node: AstNode;
  readonly rule: SemanticRule;
}

const keyOf = (nodeId: number, attr: string): string => `${nodeId}.${attr}`;

function resolveTarget(node: AstNode, ref: AttrRef): AstNode {
  switch (ref.on) {
    case 'self':
      return node;
    case 'parent':
      if (!node.parent) {
        throw new Error(`attribute '${ref.attr}': ${node} has no parent`);
      }
      return node.parent;
    case 'child': {
      const c = node.children[ref.index];
      if (!c) {
        throw new Error(`attribute '${ref.attr}': ${node} has no child #${ref.index}`);
      }
      return c;
    }
    case 'sibling': {
      if (!node.parent) {
        throw new Error(`attribute '${ref.attr}': ${node} has no parent (no siblings)`);
      }
      const s = node.parent.children[node.childIndex + ref.offset];
      if (!s) {
        throw new Error(`attribute '${ref.attr}': ${node} has no sibling at offset ${ref.offset}`);
      }
      return s;
    }
  }
}

/**
 * Instantiates every attribute of every AST node, builds the dependency
 * graph between attribute instances, topologically sorts it (Kahn's
 * algorithm) and evaluates the attributes in that order.
 */
export class Evaluator {
  constructor(private readonly grammar: AttributeGrammar) {}

  evaluate(root: AstNode): EvaluationResult {
    const nodes: AstNode[] = [];
    const visit = (n: AstNode): void => {
      nodes.push(n);
      n.children.forEach(visit);
    };
    visit(root);

    // 1. Instantiate one attribute instance per (node, rule) pair.
    const instances = new Map<string, Instance>();
    for (const node of nodes) {
      for (const rule of this.grammar.rulesFor(node.kind)) {
        instances.set(keyOf(node.id, rule.attr), { node, rule });
      }
    }

    // 2. Resolve declared dependencies into edges between instances.
    const depsOf = new Map<string, Set<string>>();
    for (const [key, inst] of instances) {
      const deps = new Set<string>();
      for (const ref of inst.rule.dependsOn(inst.node)) {
        const target = resolveTarget(inst.node, ref);
        const depKey = keyOf(target.id, ref.attr);
        if (!instances.has(depKey)) {
          throw new UndefinedAttributeError(
            `${inst.node}.${inst.rule.attr} depends on undefined attribute ` +
              `${target}.${ref.attr} (no rule defines it)`,
          );
        }
        deps.add(depKey);
      }
      depsOf.set(key, deps);
    }

    // 3. Kahn's topological sort: dependencies are evaluated first.
    const dependents = new Map<string, string[]>();
    const indegree = new Map<string, number>();
    for (const [key, deps] of depsOf) {
      indegree.set(key, deps.size);
      for (const dep of deps) {
        const list = dependents.get(dep) ?? [];
        list.push(key);
        dependents.set(dep, list);
      }
    }
    const queue = [...instances.keys()].filter((k) => indegree.get(k) === 0);
    const order: string[] = [];
    const done = new Set<string>();
    while (queue.length > 0) {
      const key = queue.shift()!;
      if (done.has(key)) continue;
      done.add(key);
      order.push(key);
      for (const dependent of dependents.get(key) ?? []) {
        const remaining = indegree.get(dependent)! - 1;
        indegree.set(dependent, remaining);
        if (remaining === 0) queue.push(dependent);
      }
    }
    if (order.length < instances.size) {
      const remaining = new Set([...instances.keys()].filter((k) => !done.has(k)));
      const cycle = findCycle(remaining, depsOf);
      throw new CycleError(cycle.map((k) => describeInstance(k, instances)));
    }

    // 4. Evaluate attributes in topological order.
    const values = new Map<string, unknown>();
    const diagnostics: Diagnostic[] = [];
    for (const key of order) {
      const inst = instances.get(key)!;
      const ctx: RuleContext = {
        get: <T,>(ref: AttrRef): T => {
          const target = resolveTarget(inst.node, ref);
          return values.get(keyOf(target.id, ref.attr)) as T;
        },
        report: (message: string, pos?: SourcePos): void => {
          diagnostics.push({ message, pos: pos ?? inst.node.pos, nodeId: inst.node.id });
        },
      };
      values.set(key, inst.rule.compute(inst.node, ctx));
    }

    diagnostics.sort(
      (a, b) => a.pos.line - b.pos.line || a.pos.column - b.pos.column,
    );

    return {
      get<T,>(node: AstNode, attr: string): T {
        const key = keyOf(node.id, attr);
        if (!values.has(key)) {
          throw new UndefinedAttributeError(`${node}.${attr} was not evaluated`);
        }
        return values.get(key) as T;
      },
      has(node: AstNode, attr: string): boolean {
        return values.has(keyOf(node.id, attr));
      },
      diagnostics,
      evaluationOrder: order,
    };
  }
}

function describeInstance(key: string, instances: Map<string, Instance>): string {
  const inst = instances.get(key);
  return inst ? `${inst.node}.${inst.rule.attr}` : key;
}

/** DFS over the remaining (cyclic) subgraph to extract one concrete cycle. */
function findCycle(remaining: Set<string>, depsOf: Map<string, Set<string>>): string[] {
  const state = new Map<string, 'visiting' | 'done'>();
  const stack: string[] = [];
  const dfs = (key: string): string[] | null => {
    state.set(key, 'visiting');
    stack.push(key);
    for (const dep of depsOf.get(key) ?? []) {
      if (!remaining.has(dep)) continue;
      if (state.get(dep) === 'visiting') {
        return [...stack.slice(stack.indexOf(dep)), dep];
      }
      if (!state.has(dep)) {
        const found = dfs(dep);
        if (found) return found;
      }
    }
    stack.pop();
    state.set(key, 'done');
    return null;
  };
  for (const key of remaining) {
    if (!state.has(key)) {
      const found = dfs(key);
      if (found) return found;
    }
  }
  return [...remaining];
}
