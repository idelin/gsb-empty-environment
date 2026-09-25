import type { ImportNamespaces, ModuleSpec, VersionVector } from './types.ts';
import { vectorKey } from './types.ts';

export type LinkErrorCode =
  | 'MODULE_NOT_FOUND'
  | 'VERSION_MISMATCH'
  | 'MISSING_EXPORT'
  | 'SIGNATURE_MISMATCH'
  | 'EVAL_CYCLE'
  | 'INIT_FAILED'
  | 'EXPORT_NOT_PRODUCED'
  | 'DUPLICATE_MODULE';

export class LinkError extends Error {
  readonly code: LinkErrorCode;
  readonly detail?: unknown;
  constructor(code: LinkErrorCode, message: string, detail?: unknown) {
    super(message);
    this.name = 'LinkError';
    this.code = code;
    this.detail = detail;
  }
}

export interface ModuleRef {
  readonly name: string;
  readonly version: string;
}

export interface ModuleRecord {
  readonly spec: ModuleSpec;
  readonly exports: Readonly<Record<string, unknown>>;
  readonly vector: VersionVector;
}

export type LinkResult =
  | {
      readonly ok: true;
      readonly vector: VersionVector;
      readonly cacheKey: string;
      readonly records: ReadonlyMap<string, ModuleRecord>;
      readonly fromCache: boolean;
    }
  | { readonly ok: false; readonly errors: readonly LinkError[] };

/** Consistent view of which module versions were live when a decision was made. */
export interface LivenessSnapshot {
  readonly decidedAt: number;
  readonly cacheKey: string | null;
  readonly live: Readonly<Record<string, string>>;
}

export type HmrResult =
  | {
      readonly ok: true;
      readonly previous: LivenessSnapshot;
      readonly current: { readonly cacheKey: string; readonly live: Readonly<Record<string, string>> };
    }
  | { readonly ok: false; readonly errors: readonly LinkError[]; readonly snapshot: LivenessSnapshot; readonly rolledBack: true };

interface Graph {
  readonly decl: Map<string, Set<string>>;
  readonly eval: Map<string, Set<string>>;
}

function specKey(name: string, version: string): string {
  return `${name}@${version}`;
}

/** Shortest cycle in a directed graph, returned as a node chain ending where it started. */
export function shortestCycle(graph: ReadonlyMap<string, ReadonlySet<string>>): string[] | null {
  let best: string[] | null = null;
  for (const start of graph.keys()) {
    const visited = new Set<string>([start]);
    const queue: string[][] = [[start]];
    while (queue.length > 0) {
      const path = queue.shift()!;
      if (best !== null && path.length + 1 >= best.length) continue;
      const node = path[path.length - 1];
      for (const next of graph.get(node) ?? []) {
        if (next === start) {
          const cycle = [...path, start];
          if (best === null || cycle.length < best.length) best = cycle;
        } else if (!visited.has(next)) {
          visited.add(next);
          queue.push([...path, next]);
        }
      }
    }
  }
  return best;
}

export class Registry {
  private readonly specs = new Map<string, ModuleSpec>();
  private readonly cache = new Map<string, Map<string, ModuleRecord>>();
  private liveCacheKey: string | null = null;

  register(spec: ModuleSpec): void {
    const key = specKey(spec.name, spec.version);
    if (this.specs.has(key)) {
      throw new LinkError('DUPLICATE_MODULE', `module '${key}' is already registered`);
    }
    for (const [name, decl] of Object.entries(spec.exports)) {
      if (decl.kind === 'decl' && !(spec.declare && name in spec.declare)) {
        throw new LinkError(
          'EXPORT_NOT_PRODUCED',
          `module '${key}' declares export '${name}' as 'decl' but provides no value in 'declare'`,
        );
      }
    }
    this.specs.set(key, spec);
  }

  get cacheSize(): number {
    return this.cache.size;
  }

  cacheKeys(): string[] {
    return [...this.cache.keys()];
  }

  /** Liveness decision snapshot: the currently live version vector. */
  livenessSnapshot(): LivenessSnapshot {
    const records = this.liveCacheKey === null ? undefined : this.cache.get(this.liveCacheKey);
    const live: Record<string, string> = {};
    if (records) {
      for (const [name, record] of records) live[name] = record.spec.version;
    }
    return { decidedAt: Date.now(), cacheKey: this.liveCacheKey, live };
  }

  /**
   * Atomic batch link. Validation (resolution, signatures, eval-cycle check) fully
   * completes before any module initializes; any failure discards the whole batch
   * and leaves the live set and cache untouched.
   */
  link(roots: readonly ModuleRef[]): LinkResult {
    const closure = this.resolveClosure(roots);
    if (Array.isArray(closure)) return { ok: false, errors: closure };
    const { specs, vector } = closure;
    const key = vectorKey(vector);

    const cached = this.cache.get(key);
    if (cached) {
      this.liveCacheKey = key;
      return { ok: true, vector, cacheKey: key, records: cached, fromCache: true };
    }

    const signatureErrors = this.checkSignatures(specs);
    if (signatureErrors.length > 0) return { ok: false, errors: signatureErrors };

    const graph = buildGraphs(specs);
    const cycle = shortestCycle(graph.eval);
    if (cycle !== null) {
      return {
        ok: false,
        errors: [
          new LinkError('EVAL_CYCLE', `evaluation cycle detected: ${cycle.join(' -> ')}`, { chain: cycle }),
        ],
      };
    }

    const records = new Map<string, ModuleRecord>();
    for (const name of topoOrder(graph.eval)) {
      const spec = specs.get(name)!;
      const imports = this.buildImportNamespaces(spec, specs, records);
      let produced: Record<string, unknown> = {};
      if (spec.init) {
        try {
          produced = spec.init(imports);
        } catch (cause) {
          return {
            ok: false,
            errors: [
              new LinkError(
                'INIT_FAILED',
                `module '${specKey(spec.name, spec.version)}' failed to initialize: ${(cause as Error).message}`,
                { module: spec.name, version: spec.version, cause },
              ),
            ],
          };
        }
      }
      const exports: Record<string, unknown> = { ...spec.declare, ...produced };
      for (const [exportName, decl] of Object.entries(spec.exports)) {
        if (decl.kind === 'value' && !(exportName in produced)) {
          return {
            ok: false,
            errors: [
              new LinkError(
                'EXPORT_NOT_PRODUCED',
                `module '${specKey(spec.name, spec.version)}' did not produce value export '${exportName}'`,
                { module: spec.name, exportName },
              ),
            ],
          };
        }
      }
      records.set(name, { spec, exports: Object.freeze(exports), vector });
    }

    this.cache.set(key, records);
    this.liveCacheKey = key;
    return { ok: true, vector, cacheKey: key, records, fromCache: false };
  }

  /**
   * Hot replacement: takes a liveness snapshot, links the new batch, and only then
   * swaps the live vector. On failure the previous live set keeps serving (rollback).
   * Old cache entries are retained, so closures captured from old versions keep working.
   */
  hmrReplace(roots: readonly ModuleRef[]): HmrResult {
    const snapshot = this.livenessSnapshot();
    const result = this.link(roots);
    if (!result.ok) {
      return { ok: false, errors: result.errors, snapshot, rolledBack: true };
    }
    const live: Record<string, string> = {};
    for (const [name, record] of result.records) live[name] = record.spec.version;
    return { ok: true, previous: snapshot, current: { cacheKey: result.cacheKey, live } };
  }

  /** Read an export value from the live batch. */
  getExport(moduleName: string, exportName: string): unknown {
    if (this.liveCacheKey === null) throw new LinkError('MODULE_NOT_FOUND', 'no live batch');
    const record = this.cache.get(this.liveCacheKey)!.get(moduleName);
    if (!record) throw new LinkError('MODULE_NOT_FOUND', `module '${moduleName}' is not live`);
    if (!(exportName in record.exports)) {
      throw new LinkError('MISSING_EXPORT', `module '${moduleName}' has no export '${exportName}'`);
    }
    return record.exports[exportName];
  }

  private resolveClosure(
    roots: readonly ModuleRef[],
  ): { specs: Map<string, ModuleSpec>; vector: VersionVector } | LinkError[] {
    const errors: LinkError[] = [];
    const specs = new Map<string, ModuleSpec>();
    const vector: VersionVector = {};
    const queue: ModuleRef[] = [...roots];
    while (queue.length > 0) {
      const ref = queue.shift()!;
      if (ref.name in vector) {
        if (vector[ref.name] !== ref.version) {
          errors.push(
            new LinkError(
              'VERSION_MISMATCH',
              `module '${ref.name}' required at both '${vector[ref.name]}' and '${ref.version}'`,
              { module: ref.name, wanted: [vector[ref.name], ref.version] },
            ),
          );
        }
        continue;
      }
      const spec = this.specs.get(specKey(ref.name, ref.version));
      if (!spec) {
        const available = [...this.specs.keys()]
          .filter((k) => k.startsWith(`${ref.name}@`))
          .map((k) => k.slice(ref.name.length + 1));
        const code: LinkErrorCode = available.length > 0 ? 'VERSION_MISMATCH' : 'MODULE_NOT_FOUND';
        errors.push(
          new LinkError(
            code,
            code === 'VERSION_MISMATCH'
              ? `module '${ref.name}' required at version '${ref.version}' but only [${available.join(', ')}] registered`
              : `module '${ref.name}' is not registered`,
            { module: ref.name, wanted: ref.version, available },
          ),
        );
        continue;
      }
      vector[ref.name] = ref.version;
      specs.set(ref.name, spec);
      for (const imp of spec.imports) queue.push({ name: imp.from, version: imp.version });
    }
    return errors.length > 0 ? errors : { specs, vector };
  }

  private checkSignatures(specs: ReadonlyMap<string, ModuleSpec>): LinkError[] {
    const errors: LinkError[] = [];
    for (const spec of specs.values()) {
      spec.imports.forEach((imp, importIndex) => {
        const target = specs.get(imp.from)!;
        for (const [name, expected] of Object.entries(imp.names)) {
          const exported = target.exports[name];
          const where = `module '${spec.name}' import statement #${importIndex} (from '${imp.from}@${imp.version}')`;
          if (!exported) {
            errors.push(
              new LinkError('MISSING_EXPORT', `${where}: export '${name}' not found`, {
                module: spec.name,
                importIndex,
                from: imp.from,
                name,
              }),
            );
          } else if (exported.signature !== expected) {
            errors.push(
              new LinkError(
                'SIGNATURE_MISMATCH',
                `${where}: signature mismatch for '${name}': expected '${expected}', got '${exported.signature}'`,
                { module: spec.name, importIndex, from: imp.from, name, expected, actual: exported.signature },
              ),
            );
          }
        }
      });
    }
    return errors;
  }

  private buildImportNamespaces(
    spec: ModuleSpec,
    specs: ReadonlyMap<string, ModuleSpec>,
    records: ReadonlyMap<string, ModuleRecord>,
  ): ImportNamespaces {
    const namespaces: ImportNamespaces = {};
    for (const imp of spec.imports) {
      const target = specs.get(imp.from)!;
      const ns: Record<string, unknown> = {};
      for (const name of Object.keys(imp.names)) {
        const decl = target.exports[name];
        if (decl.kind === 'decl') {
          ns[name] = target.declare![name];
        } else {
          ns[name] = records.get(imp.from)!.exports[name];
        }
      }
      namespaces[imp.from] = Object.freeze(ns);
    }
    return namespaces;
  }
}

function buildGraphs(specs: ReadonlyMap<string, ModuleSpec>): Graph {
  const decl = new Map<string, Set<string>>();
  const evalG = new Map<string, Set<string>>();
  for (const name of specs.keys()) {
    decl.set(name, new Set());
    evalG.set(name, new Set());
  }
  for (const spec of specs.values()) {
    for (const imp of spec.imports) {
      const target = specs.get(imp.from)!;
      for (const name of Object.keys(imp.names)) {
        const edge = target.exports[name].kind === 'decl' ? decl : evalG;
        edge.get(spec.name)!.add(imp.from);
      }
    }
  }
  return { decl, eval: evalG };
}

/** Dependencies-first order of the evaluation graph (Kahn). */
function topoOrder(evalGraph: ReadonlyMap<string, ReadonlySet<string>>): string[] {
  const dependents = new Map<string, Set<string>>();
  const remaining = new Map<string, number>();
  for (const [name, deps] of evalGraph) {
    dependents.set(name, new Set());
    remaining.set(name, deps.size);
  }
  for (const [name, deps] of evalGraph) {
    for (const dep of deps) dependents.get(dep)!.add(name);
  }
  const ready = [...remaining.entries()].filter(([, n]) => n === 0).map(([name]) => name);
  const order: string[] = [];
  while (ready.length > 0) {
    const name = ready.shift()!;
    order.push(name);
    for (const dependent of dependents.get(name)!) {
      const left = remaining.get(dependent)! - 1;
      remaining.set(dependent, left);
      if (left === 0) ready.push(dependent);
    }
  }
  return order;
}
