import type { Schema, ScalarType } from './schema.ts';

export class CompileError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CompileError';
  }
}

export type Node =
  | { id: number; kind: 'object'; schemaPath: string; props: ReadonlyMap<string, number>; required: readonly string[] }
  | { id: number; kind: 'array'; schemaPath: string; items: number }
  | { id: number; kind: 'union'; schemaPath: string; options: readonly number[] }
  | { id: number; kind: 'scalar'; schemaPath: string; scalar: ScalarType };

/** The compiled, shared validation graph. Recursion is expressed as node-id back-edges. */
export interface Graph {
  nodes: Node[];
  root: number;
}

const SCALARS: ReadonlySet<string> = new Set(['string', 'number', 'boolean', 'null']);

function escapeSegment(seg: string): string {
  return seg.replace(/~/g, '~0').replace(/\//g, '~1');
}

function unescapeSegment(seg: string): string {
  return seg.replace(/~1/g, '/').replace(/~0/g, '~');
}

/**
 * Compiles a schema into a shared graph:
 * - every schema path is compiled at most once, so `$ref`s to the same
 *   definition (including recursive self-references) share a single node;
 * - dangling references are rejected immediately;
 * - reference cycles that consume no instance (pure `$ref` chains, or cycles
 *   through `oneOf` options only) are rejected as non-productive.
 */
export function compile(rootSchema: Schema): Graph {
  const nodes: Node[] = [];
  const idByPath = new Map<string, number>();
  const resolvingRefs = new Set<string>();

  function resolvePointer(ref: string): { path: string; schema: unknown } {
    if (ref !== '#' && !ref.startsWith('#/')) {
      throw new CompileError(`unsupported non-local reference "${ref}"`);
    }
    const pointer = ref.slice(1); // '' or '/a/b'
    let cur: unknown = rootSchema;
    if (pointer !== '') {
      for (const raw of pointer.slice(1).split('/')) {
        const seg = unescapeSegment(raw);
        if (cur === null || typeof cur !== 'object' || !(seg in (cur as Record<string, unknown>))) {
          throw new CompileError(`dangling reference "${ref}"`);
        }
        cur = (cur as Record<string, unknown>)[seg];
      }
    }
    return { path: pointer, schema: cur };
  }

  function compileAt(path: string, schema: unknown): number {
    const known = idByPath.get(path);
    if (known !== undefined) return known;
    if (schema === null || typeof schema !== 'object' || Array.isArray(schema)) {
      throw new CompileError(`unsupported schema at #${path}: expected a schema object`);
    }
    const s = schema as Record<string, unknown>;

    if ('$ref' in s) {
      if (typeof s.$ref !== 'string') throw new CompileError(`invalid $ref at #${path}`);
      if (resolvingRefs.has(path)) {
        throw new CompileError(
          `non-productive reference cycle at #${path}: the reference chain consumes no instance`,
        );
      }
      resolvingRefs.add(path);
      try {
        const target = resolvePointer(s.$ref);
        const id = compileAt(target.path, target.schema);
        idByPath.set(path, id);
        return id;
      } finally {
        resolvingRefs.delete(path);
      }
    }

    const id = nodes.length;
    idByPath.set(path, id);
    nodes.push(undefined as unknown as Node); // reserve the id so recursion can share it

    if (s.oneOf !== undefined) {
      if (!Array.isArray(s.oneOf) || s.oneOf.length === 0) {
        throw new CompileError(`invalid oneOf at #${path}: expected a non-empty array`);
      }
      const options = s.oneOf.map((branch, i) => compileAt(`${path}/oneOf/${i}`, branch));
      nodes[id] = { id, kind: 'union', schemaPath: `#${path}`, options };
    } else if (s.type === 'object') {
      const rawRequired = s.required ?? [];
      if (!Array.isArray(rawRequired) || rawRequired.some((r) => typeof r !== 'string')) {
        throw new CompileError(`invalid required at #${path}: expected an array of strings`);
      }
      const rawProps = s.properties ?? {};
      if (rawProps === null || typeof rawProps !== 'object' || Array.isArray(rawProps)) {
        throw new CompileError(`invalid properties at #${path}: expected an object`);
      }
      const props = new Map<string, number>();
      for (const [key, child] of Object.entries(rawProps as Record<string, unknown>)) {
        props.set(key, compileAt(`${path}/properties/${escapeSegment(key)}`, child));
      }
      nodes[id] = { id, kind: 'object', schemaPath: `#${path}`, props, required: rawRequired as string[] };
    } else if (s.type === 'array') {
      if (!('items' in s)) {
        throw new CompileError(`array schema at #${path} requires "items"`);
      }
      const items = compileAt(`${path}/items`, s.items);
      nodes[id] = { id, kind: 'array', schemaPath: `#${path}`, items };
    } else if (SCALARS.has(s.type as string)) {
      nodes[id] = { id, kind: 'scalar', schemaPath: `#${path}`, scalar: s.type as ScalarType };
    } else {
      throw new CompileError(
        `unsupported schema construct at #${path} (supported: object, array, oneOf, $ref, string|number|boolean|null)`,
      );
    }
    return id;
  }

  const root = compileAt('', rootSchema);
  const graph: Graph = { nodes, root };
  assertNoNonConsumingCycles(graph);
  return graph;
}

/**
 * Rejects cycles made exclusively of union-option edges. Such a cycle would
 * expand forever without consuming any instance. Cycles that pass through an
 * object property or an array item are productive and allowed.
 */
function assertNoNonConsumingCycles(graph: Graph): void {
  const state = new Array<number>(graph.nodes.length).fill(0); // 0=white 1=gray 2=black
  const stack: number[] = [];

  function dfs(id: number): void {
    state[id] = 1;
    stack.push(id);
    const node = graph.nodes[id];
    const next: readonly number[] = node.kind === 'union' ? node.options : [];
    for (const m of next) {
      if (state[m] === 1) {
        const cycle = stack
          .slice(stack.indexOf(m))
          .concat(m)
          .map((n) => graph.nodes[n].schemaPath)
          .join(' -> ');
        throw new CompileError(`non-consuming reference cycle through oneOf branches: ${cycle}`);
      }
      if (state[m] === 0) dfs(m);
    }
    stack.pop();
    state[id] = 2;
  }

  for (let i = 0; i < graph.nodes.length; i++) {
    if (state[i] === 0) dfs(i);
  }
}
