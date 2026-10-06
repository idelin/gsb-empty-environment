import {
  AliasConflictError,
  CycleError,
  DeletedTargetError,
  NotFoundError,
  TreeError,
} from "./errors";
import { canonicalPath, isPrefixPath, normalizePath, splitPath } from "./pathutil";
import { Tree } from "./tree";

export interface AliasResolution {
  path: string;
  hops: number;
  chain: string[];
}

/**
 * Path aliases over a tree. Alias targets are stable node ids: moving a node
 * updates its real path, but alias entries keep pointing at the same id.
 * Resolution is a bounded-hop interpreter: each hop rewrites the longest
 * matching alias prefix to the target's current real path, and resolution
 * aborts with CycleError once the hop budget is exhausted.
 */
export class AliasTable {
  private readonly entries = new Map<string, string>();

  constructor(
    private readonly tree: Tree,
    private readonly maxHops: number = 32,
    private readonly onChange: () => void = () => {},
  ) {}

  has(aliasPath: string): boolean {
    return this.entries.has(canonicalPath(aliasPath));
  }

  targetOf(aliasPath: string): string | undefined {
    return this.entries.get(canonicalPath(aliasPath));
  }

  list(): Array<{ aliasPath: string; targetId: string }> {
    return [...this.entries.entries()].map(([aliasPath, targetId]) => ({
      aliasPath,
      targetId,
    }));
  }

  add(aliasPath: string, targetId: string): void {
    if (splitPath(aliasPath).includes(".."))
      throw new TreeError(`alias path must not contain '..': ${aliasPath}`);
    const key = canonicalPath(aliasPath);
    if (key === "/") throw new TreeError("cannot alias the root");
    if (this.entries.has(key))
      throw new AliasConflictError(`alias already defined: ${key}`);
    if (!this.tree.has(targetId))
      throw new NotFoundError(`alias target does not exist: ${targetId}`);
    this.entries.set(key, targetId);
    try {
      // A new alias can only create a resolution cycle that passes through
      // its own path, so interpreting that path is sufficient validation.
      this.resolve(key);
    } catch (err) {
      this.entries.delete(key);
      throw err;
    }
    this.onChange();
  }

  remove(aliasPath: string): boolean {
    const removed = this.entries.delete(canonicalPath(aliasPath));
    if (removed) this.onChange();
    return removed;
  }

  private longestPrefixMatch(path: string): string | null {
    let best: string | null = null;
    for (const key of this.entries.keys()) {
      if (!isPrefixPath(key, path)) continue;
      if (best === null || key.length > best.length) best = key;
    }
    return best;
  }

  resolve(path: string): AliasResolution {
    // '..' segments are preserved so the caller (e.g. the mount graph) can
    // enforce root boundaries instead of silently clamping them here.
    let current = normalizePath(path);
    const visited = new Set<string>();
    const chain: string[] = [];
    let hops = 0;
    for (;;) {
      const match = this.longestPrefixMatch(current);
      if (match === null) return { path: current, hops, chain };
      if (hops >= this.maxHops)
        throw new CycleError(
          `alias resolution exceeded ${this.maxHops} hops at ${current}`,
        );
      if (visited.has(current))
        throw new CycleError(`alias resolution loop at ${current}`);
      visited.add(current);
      const targetId = this.entries.get(match)!;
      if (!this.tree.has(targetId))
        throw new DeletedTargetError(
          `alias ${match} targets deleted node ${targetId}`,
        );
      const suffix = current.slice(match.length);
      const next = normalizePath(this.tree.realPath(targetId) + suffix);
      // An alias whose target's real path equals the matched prefix is a
      // fixed point: the path is already real, so interpretation stops.
      if (next === current) return { path: current, hops, chain };
      current = next;
      hops++;
      chain.push(match);
    }
  }

  resolveId(path: string): { id: string; resolution: AliasResolution } {
    const resolution = this.resolve(path);
    const id = this.tree.idAtPath(resolution.path);
    if (id === null)
      throw new NotFoundError(`no node at resolved path ${resolution.path}`);
    return { id, resolution };
  }
}
