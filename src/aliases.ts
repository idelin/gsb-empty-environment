import { VfsError, ERR } from './errors.ts';
import { splitPath, joinPath, isValidName } from './pathutil.ts';

export interface AliasMatch {
  alias: string;
  targetId: string;
  rest: string[];
}

/**
 * alias path -> target node id. Targets are ids (not paths), so moving a
 * node never rewrites this table; a removed node leaves a dangling alias
 * that the resolver reports as ALIAS_TARGET_MISSING.
 */
export class AliasTable {
  private table = new Map<string, string>();
  private onChange: () => void;

  constructor(onChange: () => void = () => {}) {
    this.onChange = onChange;
  }

  set(path: string, targetId: string): void {
    const norm = normalizeAliasPath(path);
    if (this.table.has(norm)) {
      throw new VfsError(ERR.AliasConflict, `alias ${norm} is already defined`);
    }
    this.table.set(norm, targetId);
    this.onChange();
  }

  remove(path: string): boolean {
    const removed = this.table.delete(normalizeAliasPath(path));
    if (removed) this.onChange();
    return removed;
  }

  has(path: string): boolean {
    return this.table.has(normalizeAliasPath(path));
  }

  targetOf(path: string): string | undefined {
    return this.table.get(normalizeAliasPath(path));
  }

  /** Longest-prefix match on segment boundaries; returns the remaining segments. */
  match(segs: string[]): AliasMatch | null {
    for (let k = segs.length; k >= 1; k--) {
      const prefix = joinPath(segs.slice(0, k));
      const targetId = this.table.get(prefix);
      if (targetId !== undefined) {
        return { alias: prefix, targetId, rest: segs.slice(k) };
      }
    }
    return null;
  }

  entries(): Array<[string, string]> {
    return [...this.table.entries()];
  }
}

export function normalizeAliasPath(path: string): string {
  const segs = splitPath(path);
  if (segs.length === 0) {
    throw new VfsError(ERR.InvalidPath, 'alias path cannot be the root');
  }
  for (const seg of segs) {
    if (!isValidName(seg)) {
      throw new VfsError(ERR.InvalidPath, `invalid alias path: ${path}`);
    }
  }
  return joinPath(segs);
}
