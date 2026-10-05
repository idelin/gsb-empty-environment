/**
 * Incremental, snapshot-based project state.
 *
 * `Project` owns the current module set. Every mutation (`setModule` /
 * `removeModule`) produces a new immutable `Snapshot`; previously returned
 * snapshots stay queryable forever and never change.
 *
 * Resolution results are cached per snapshot and keyed by (module, name).
 * Each cache entry records the exact set of modules consulted while computing
 * it, so a mutation only invalidates entries whose dependency set contains the
 * changed module — unaffected symbols keep their cached results (structurally
 * shared with the parent snapshot, so old snapshots are unaffected too).
 */
import type { Diagnostic, ModuleSpec, Resolution } from './types.ts';
import { cacheKey, computeResolution, formatChain, type CacheEntry } from './resolver.ts';

export interface SnapshotStats {
  /** Resolutions computed from scratch in this snapshot. */
  computed: number;
  /** Cache hits (including entries inherited from parent snapshots). */
  reused: number;
}

export class Snapshot {
  readonly stats: SnapshotStats = { computed: 0, reused: 0 };
  readonly modules: ReadonlyMap<string, ModuleSpec>;
  readonly version: number;
  readonly cache: Map<string, CacheEntry>;

  constructor(modules: ReadonlyMap<string, ModuleSpec>, version: number, cache: Map<string, CacheEntry>) {
    this.modules = modules;
    this.version = version;
    this.cache = cache;
  }

  /** Resolve the symbol exported as `name` by `module`. */
  resolveExport(module: string, name: string): Resolution {
    const key = cacheKey(module, name);
    const hit = this.cache.get(key);
    if (hit) {
      this.stats.reused++;
      return hit.result;
    }
    const { result, deps } = computeResolution(this.modules, module, name);
    this.cache.set(key, { result, deps });
    this.stats.computed++;
    return result;
  }

  /** Resolve a name explicitly imported by `module`. Throws if not imported. */
  resolveImport(module: string, name: string): Resolution {
    const spec = this.modules.get(module);
    const imp = spec?.imports?.find((i) => i.name === name);
    if (!imp) {
      throw new Error(`module '${module}' does not import '${name}'`);
    }
    return this.resolveExport(imp.from, imp.name);
  }

  /** Resolve every name exported by `module` (definitions + re-exports). */
  listExports(module: string): Map<string, Resolution> {
    const out = new Map<string, Resolution>();
    const spec = this.modules.get(module);
    if (!spec) return out;
    const names = new Set<string>();
    for (const d of spec.definitions ?? []) names.add(d);
    for (const re of spec.reexports ?? []) names.add(re.as ?? re.name);
    for (const n of names) out.set(n, this.resolveExport(module, n));
    return out;
  }

  /** All problems reachable from `module`: broken imports/re-exports, conflicts. */
  diagnostics(module: string): Diagnostic[] {
    const spec = this.modules.get(module);
    if (!spec) return [];
    const diags: Diagnostic[] = [];
    for (const imp of spec.imports ?? []) {
      const r = this.resolveExport(imp.from, imp.name);
      if (r.status !== 'resolved') {
        diags.push({
          module,
          kind: 'unresolved-import',
          name: imp.name,
          from: imp.from,
          message: `import { ${imp.name} } from '${imp.from}' in '${module}' does not resolve (${r.status})`,
          resolution: r,
        });
      }
    }
    for (const re of spec.reexports ?? []) {
      const r = this.resolveExport(re.from, re.name);
      if (r.status !== 'resolved') {
        const kind =
          r.status === 'circular'
            ? 'circular-reexport'
            : r.status === 'conflict'
              ? 'conflicting-export'
              : 'unresolved-reexport';
        diags.push({
          module,
          kind,
          name: re.as ?? re.name,
          from: re.from,
          message: `re-export { ${re.name}${re.as ? ` as ${re.as}` : ''} } from '${re.from}' in '${module}' does not resolve (${r.status})`,
          resolution: r,
        });
      }
    }
    for (const [name, r] of this.listExports(module)) {
      if (r.status === 'conflict') {
        const [a, b] = r.definitions;
        diags.push({
          module,
          kind: 'conflicting-export',
          name,
          message:
            `export '${name}' of '${module}' is ambiguous:\n` +
            `  chain 1: ${formatChain(a.chain)}\n` +
            `  chain 2: ${formatChain(b.chain)}`,
          resolution: r,
        });
      }
    }
    return diags;
  }

  moduleIds(): string[] {
    return [...this.modules.keys()];
  }

  moduleSpec(id: string): ModuleSpec | undefined {
    return this.modules.get(id);
  }
}

function freezeSpec(spec: ModuleSpec): ModuleSpec {
  return Object.freeze({
    definitions: Object.freeze([...(spec.definitions ?? [])]),
    imports: Object.freeze((spec.imports ?? []).map((i) => Object.freeze({ ...i }))),
    reexports: Object.freeze((spec.reexports ?? []).map((r) => Object.freeze({ ...r }))),
  });
}

export class Project {
  #modules: Map<string, ModuleSpec> = new Map();
  #snapshot: Snapshot = new Snapshot(this.#modules, 0, new Map());
  #version = 0;

  get version(): number {
    return this.#version;
  }

  /** The current (latest) snapshot. */
  snapshot(): Snapshot {
    return this.#snapshot;
  }

  /** Add or replace a module; returns the new snapshot. */
  setModule(id: string, spec: ModuleSpec): Snapshot {
    const next = new Map(this.#modules);
    next.set(id, freezeSpec(spec));
    return this.#commit(next, id);
  }

  /** Remove a module; returns the new snapshot. */
  removeModule(id: string): Snapshot {
    if (!this.#modules.has(id)) return this.#snapshot;
    const next = new Map(this.#modules);
    next.delete(id);
    return this.#commit(next, id);
  }

  /**
   * Cache keys ("module name") of already-computed symbols whose resolution
   * depends on `moduleId` — i.e. the symbols a mutation of `moduleId` affects.
   */
  affectedSymbols(moduleId: string): string[] {
    const out: string[] = [];
    for (const [key, entry] of this.#snapshot.cache) {
      if (entry.deps.has(moduleId)) out.push(key);
    }
    return out.sort();
  }

  #commit(nextModules: Map<string, ModuleSpec>, changedId: string): Snapshot {
    this.#modules = nextModules;
    this.#version++;
    const cache = new Map<string, CacheEntry>();
    for (const [key, entry] of this.#snapshot.cache) {
      if (!entry.deps.has(changedId)) cache.set(key, entry);
    }
    this.#snapshot = new Snapshot(nextModules, this.#version, cache);
    return this.#snapshot;
  }
}
