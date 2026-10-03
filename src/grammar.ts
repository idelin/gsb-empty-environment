/**
 * Attribute grammar evaluation kernel.
 *
 * - Attributes are declared per AST node kind as `synthesized` or `inherited`.
 * - Each attribute has exactly one rule: a static (or node-dependent) list of
 *   attribute references it depends on, plus a compute function.
 * - The evaluator expands every (node, attribute) pair into a vertex of a
 *   dependency graph, topologically sorts it (Kahn's algorithm) and evaluates
 *   rules in that order. Leftover vertices form a cycle and are reported.
 */

export interface Pos {
  line: number;
  col: number;
  offset: number;
}

export interface ASTNode {
  kind: string;
  pos: Pos;
  parent?: ASTNode;
  [field: string]: unknown;
}

export type AttrKind = 'synthesized' | 'inherited';

/** A reference to another node relative to the node a rule belongs to. */
export type NodeRef =
  | { from: 'self' }
  | { from: 'parent' }
  | { from: 'child'; field: string }
  | { from: 'childAt'; field: string; index: number }
  | { from: 'direct'; target: ASTNode };

export interface AttrRef {
  node: NodeRef;
  attr: string;
}

export const self = (attr: string): AttrRef => ({ node: { from: 'self' }, attr });
export const parent = (attr: string): AttrRef => ({ node: { from: 'parent' }, attr });
export const child = (field: string, attr: string): AttrRef => ({
  node: { from: 'child', field },
  attr,
});
export const childAt = (field: string, index: number, attr: string): AttrRef => ({
  node: { from: 'childAt', field, index },
  attr,
});
/** Reference an already-resolved node directly (e.g. "previous sibling statement"). */
export const direct = (target: ASTNode, attr: string): AttrRef => ({
  node: { from: 'direct', target },
  attr,
});

export type DepList = AttrRef[] | ((node: ASTNode) => AttrRef[]);

export interface Rule {
  attr: string;
  deps: DepList;
  compute: (node: ASTNode, get: (ref: AttrRef) => unknown) => unknown;
}

export class CycleError extends Error {
  readonly cycle: string[];
  constructor(cycle: string[]) {
    super(`circular attribute dependency detected: ${cycle.join(' -> ')}`);
    this.name = 'CycleError';
    this.cycle = cycle;
  }
}

function isASTNode(value: unknown): value is ASTNode {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as ASTNode).kind === 'string' &&
    typeof (value as ASTNode).pos === 'object'
  );
}

/** Attach parent pointers by walking child fields (objects and arrays). */
export function attachParents(node: ASTNode, parentNode?: ASTNode): void {
  if (parentNode) node.parent = parentNode;
  for (const key of Object.keys(node)) {
    if (key === 'parent' || key === 'kind' || key === 'pos') continue;
    const value = node[key];
    if (isASTNode(value)) {
      attachParents(value, node);
    } else if (Array.isArray(value)) {
      for (const item of value) {
        if (isASTNode(item)) attachParents(item, node);
      }
    }
  }
}

function resolveRef(node: ASTNode, ref: NodeRef): ASTNode {
  switch (ref.from) {
    case 'self':
      return node;
    case 'parent':
      if (!node.parent) {
        throw new Error(`attribute rule on <${node.kind}> references parent, but node is the root`);
      }
      return node.parent;
    case 'child': {
      const value = node[ref.field];
      if (!isASTNode(value)) {
        throw new Error(`<${node.kind}>.${ref.field} is not an AST node`);
      }
      return value;
    }
    case 'childAt': {
      const value = node[ref.field];
      if (!Array.isArray(value) || !isASTNode(value[ref.index])) {
        throw new Error(`<${node.kind}>.${ref.field}[${ref.index}] is not an AST node`);
      }
      return value[ref.index];
    }
    case 'direct':
      return ref.target;
  }
}

function formatPos(pos: Pos): string {
  return `${pos.line}:${pos.col}`;
}

export class EvaluationResult {
  private readonly values: Map<ASTNode, Map<string, unknown>>;

  constructor(values: Map<ASTNode, Map<string, unknown>>) {
    this.values = values;
  }

  get<T = unknown>(node: ASTNode, attr: string): T {
    const attrs = this.values.get(node);
    if (!attrs || !attrs.has(attr)) {
      throw new Error(`attribute <${node.kind}>.${attr} was not evaluated`);
    }
    return attrs.get(attr) as T;
  }
}

interface Vertex {
  key: string;
  node: ASTNode;
  attr: string;
  rule: Rule;
  label: string;
}

export class AttributeGrammar {
  private readonly attrKinds = new Map<string, Map<string, AttrKind>>();
  private readonly rules = new Map<string, Rule>();

  addAttribute(nodeKind: string, name: string, kind: AttrKind): this {
    let attrs = this.attrKinds.get(nodeKind);
    if (!attrs) {
      attrs = new Map();
      this.attrKinds.set(nodeKind, attrs);
    }
    if (attrs.has(name)) {
      throw new Error(`attribute <${nodeKind}>.${name} is already declared`);
    }
    attrs.set(name, kind);
    return this;
  }

  addRule(
    nodeKind: string,
    attr: string,
    deps: DepList,
    compute: Rule['compute'],
  ): this {
    const declared = this.attrKinds.get(nodeKind);
    if (!declared || !declared.has(attr)) {
      throw new Error(`cannot add rule for undeclared attribute <${nodeKind}>.${attr}`);
    }
    const key = `${nodeKind}.${attr}`;
    if (this.rules.has(key)) {
      throw new Error(`rule for <${nodeKind}>.${attr} is already defined`);
    }
    this.rules.set(key, { attr, deps, compute });
    return this;
  }

  attributeKind(nodeKind: string, attr: string): AttrKind | undefined {
    return this.attrKinds.get(nodeKind)?.get(attr);
  }

  evaluate(root: ASTNode): EvaluationResult {
    attachParents(root);

    const nodes: ASTNode[] = [];
    const visit = (node: ASTNode): void => {
      nodes.push(node);
      for (const key of Object.keys(node)) {
        if (key === 'parent' || key === 'kind' || key === 'pos') continue;
        const value = node[key];
        if (isASTNode(value)) visit(value);
        else if (Array.isArray(value)) {
          for (const item of value) if (isASTNode(item)) visit(item);
        }
      }
    };
    visit(root);

    // --- Build the attribute-instance dependency graph -------------------
    const vertices = new Map<string, Vertex>();
    const vertexId = new Map<string, Vertex>();
    const nodeKeys = new Map<ASTNode, Map<string, string>>();

    for (const node of nodes) {
      const declared = this.attrKinds.get(node.kind);
      if (!declared) continue;
      let byAttr = nodeKeys.get(node);
      if (!byAttr) {
        byAttr = new Map();
        nodeKeys.set(node, byAttr);
      }
      for (const attr of declared.keys()) {
        const rule = this.rules.get(`${node.kind}.${attr}`);
        if (!rule) {
          throw new Error(`no rule defined for attribute <${node.kind}>.${attr}`);
        }
        const key = `${node.kind}.${attr}@${formatPos(node.pos)}#${vertices.size}`;
        const vertex: Vertex = {
          key,
          node,
          attr,
          rule,
          label: `<${node.kind}>.${attr} at ${formatPos(node.pos)}`,
        };
        vertices.set(key, vertex);
        vertexId.set(key, vertex);
        byAttr.set(attr, key);
      }
    }

    const keyOf = (node: ASTNode, attr: string): string => {
      const key = nodeKeys.get(node)?.get(attr);
      if (!key) {
        throw new Error(
          `reference to undeclared attribute <${node.kind}>.${attr} at ${formatPos(node.pos)}`,
        );
      }
      return key;
    };

    const edges = new Map<string, Set<string>>(); // dep -> dependents
    const indegree = new Map<string, number>();
    for (const key of vertices.keys()) {
      edges.set(key, new Set());
      indegree.set(key, 0);
    }
    for (const vertex of vertices.values()) {
      const deps = typeof vertex.rule.deps === 'function'
        ? vertex.rule.deps(vertex.node)
        : vertex.rule.deps;
      for (const ref of deps) {
        const target = resolveRef(vertex.node, ref.node);
        const depKey = keyOf(target, ref.attr);
        if (!edges.get(depKey)!.has(vertex.key)) {
          edges.get(depKey)!.add(vertex.key);
          indegree.set(vertex.key, indegree.get(vertex.key)! + 1);
        }
      }
    }

    // --- Kahn's topological sort -----------------------------------------
    const queue: string[] = [];
    for (const [key, degree] of indegree) {
      if (degree === 0) queue.push(key);
    }
    const order: string[] = [];
    for (let head = 0; head < queue.length; head++) {
      const key = queue[head];
      order.push(key);
      for (const next of edges.get(key)!) {
        const degree = indegree.get(next)! - 1;
        indegree.set(next, degree);
        if (degree === 0) queue.push(next);
      }
    }

    if (order.length < vertices.size) {
      const remaining = new Set(
        [...vertices.keys()].filter((key) => !order.includes(key)),
      );
      throw new CycleError(this.findCycle(remaining, edges, vertexId));
    }

    // --- Evaluate in topological order ------------------------------------
    const values = new Map<ASTNode, Map<string, unknown>>();
    for (const key of order) {
      const vertex = vertexId.get(key)!;
      const get = (ref: AttrRef): unknown => {
        const target = resolveRef(vertex.node, ref.node);
        const attrValues = values.get(target);
        if (!attrValues || !attrValues.has(ref.attr)) {
          throw new Error(
            `attribute <${target.kind}>.${ref.attr} is not available yet (missing dependency?)`,
          );
        }
        return attrValues.get(ref.attr);
      };
      const value = vertex.rule.compute(vertex.node, get);
      let attrValues = values.get(vertex.node);
      if (!attrValues) {
        attrValues = new Map();
        values.set(vertex.node, attrValues);
      }
      attrValues.set(vertex.attr, value);
    }

    return new EvaluationResult(values);
  }

  /** Extract one concrete cycle from the leftover subgraph for diagnostics. */
  private findCycle(
    remaining: Set<string>,
    edges: Map<string, Set<string>>,
    vertices: Map<string, Vertex>,
  ): string[] {
    const state = new Map<string, 'visiting' | 'done'>();
    const stack: string[] = [];

    const dfs = (key: string): string[] | null => {
      state.set(key, 'visiting');
      stack.push(key);
      for (const next of edges.get(key) ?? []) {
        if (!remaining.has(next)) continue;
        if (state.get(next) === 'visiting') {
          const start = stack.indexOf(next);
          return [...stack.slice(start), next];
        }
        if (!state.has(next)) {
          const found = dfs(next);
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
        if (found) return found.map((k) => vertices.get(k)!.label);
      }
    }
    // Should not happen: a non-empty leftover of Kahn's algorithm always has a cycle.
    return [...remaining].map((k) => vertices.get(k)!.label);
  }
}
