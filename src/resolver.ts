import { VfsError, ERR } from './errors.ts';
import { splitPath, joinPath } from './pathutil.ts';
import type { TreeStore } from './tree.ts';
import type { AliasTable } from './aliases.ts';
import type { MountGraph, Mount } from './mounts.ts';

export interface Resolved {
  nodeId: string;
  mount: Mount;
  /** Number of alias jumps taken. */
  hops: number;
}

/**
 * Bounded-jump interpreter for path resolution.
 *
 * Each iteration first tries the longest alias prefix; a hit rewrites the
 * path to the target node's current real path plus the leftover segments
 * and counts one jump. Without an alias, the path is confined to its
 * longest-prefix mount: ".." may not pop above the mount root. Resolution
 * stops after `fuel` jumps (ALIAS_RESOLUTION_LIMIT) or when a path is
 * revisited (ALIAS_CYCLE), so misconfigured or stale aliases can never
 * loop the interpreter.
 */
export class Resolver {
  private tree: TreeStore;
  private aliases: AliasTable;
  private mounts: MountGraph;
  private fuel: number;

  constructor(tree: TreeStore, aliases: AliasTable, mounts: MountGraph, fuel = 32) {
    this.tree = tree;
    this.aliases = aliases;
    this.mounts = mounts;
    this.fuel = fuel;
  }

  resolve(path: string, fuel = this.fuel): Resolved {
    let segs = splitPath(path);
    const visited = new Set<string>();
    let hops = 0;
    for (;;) {
      const alias = this.aliases.match(segs);
      if (alias) {
        const key = joinPath(segs);
        if (visited.has(key)) {
          throw new VfsError(ERR.AliasCycle, `alias resolution cycles at ${key}`);
        }
        visited.add(key);
        hops += 1;
        if (hops > fuel) {
          throw new VfsError(ERR.AliasResolutionLimit, `alias resolution exceeded ${fuel} jumps at ${key}`);
        }
        if (!this.tree.has(alias.targetId)) {
          throw new VfsError(ERR.AliasTargetMissing, `alias ${alias.alias} targets missing node ${alias.targetId}`);
        }
        segs = [...splitPath(this.tree.realPath(alias.targetId)), ...alias.rest];
        continue;
      }
      const { mount, rest } = this.mounts.splitMount(segs);
      const stack: string[] = [];
      for (const seg of rest) {
        if (seg === '.') continue;
        if (seg === '..') {
          if (stack.length === 0) {
            throw new VfsError(ERR.RootEscape, `path ${path} escapes mount ${mount.path}`);
          }
          stack.pop();
          continue;
        }
        stack.push(seg);
      }
      let cur = mount.rootId;
      for (const seg of stack) {
        const next = this.tree.childByName(cur, seg);
        if (next === undefined) {
          throw new VfsError(ERR.NotFound, `no such path: ${path}`);
        }
        cur = next;
      }
      return { nodeId: cur, mount, hops };
    }
  }
}
