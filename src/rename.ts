import { canonicalKey } from "./canonicalize.js";
import type { CanonicalRules } from "./canonicalize.js";
import type { KeyedModule } from "./conflicts.js";
import type { ConflictGroup, RenameAction } from "./types.js";

export interface RenamePlan {
  renames: RenameAction[];
  totalCost: number;
  finalPathOf: Map<string, string>;
  finalKeyOf: Map<string, string>;
}

function splitDirBase(path: string): { dir: string; base: string } {
  const unified = path.split("\\").join("/");
  const idx = unified.lastIndexOf("/");
  if (idx < 0) {
    return { dir: "", base: unified };
  }
  return { dir: unified.slice(0, idx), base: unified.slice(idx + 1) };
}

function splitExt(base: string, rules: CanonicalRules): { stem: string; ext: string } {
  const lower = base.toLowerCase();
  for (const ext of rules.extensions) {
    if (lower.length > ext.length && lower.endsWith(ext)) {
      return { stem: base.slice(0, base.length - ext.length), ext: base.slice(base.length - ext.length) };
    }
  }
  return { stem: base, ext: "" };
}

function comparePath(a: string, b: string): number {
  if (a < b) {
    return -1;
  }
  if (a > b) {
    return 1;
  }
  return 0;
}

function findFreeName(path: string, rules: CanonicalRules, occupancy: Map<string, number>): string {
  const parts = splitDirBase(path);
  const nameParts = splitExt(parts.base, rules);
  const prefix = parts.dir.length > 0 ? parts.dir + "/" : "";
  let n = 2;
  while (true) {
    const candidate = prefix + nameParts.stem + "_" + String(n) + nameParts.ext;
    const key = canonicalKey(candidate, rules);
    if (!occupancy.has(key)) {
      return candidate;
    }
    n++;
  }
}

export function planRenames(modules: KeyedModule[], conflicts: ConflictGroup[], rules: CanonicalRules): RenamePlan {
  const occupancy: Map<string, number> = new Map();
  const byPath: Map<string, KeyedModule> = new Map();
  const finalPathOf: Map<string, string> = new Map();
  const finalKeyOf: Map<string, string> = new Map();
  for (const m of modules) {
    occupancy.set(m.key, (occupancy.get(m.key) ?? 0) + 1);
    byPath.set(m.path, m);
    finalPathOf.set(m.path, m.path);
    finalKeyOf.set(m.path, m.key);
  }
  const renames: RenameAction[] = [];
  let totalCost = 0;
  for (const group of conflicts) {
    const members: KeyedModule[] = [];
    for (const p of group.modules) {
      const m = byPath.get(p);
      if (m) {
        members.push(m);
      }
    }
    members.sort((a: KeyedModule, b: KeyedModule) => {
      if (a.cost !== b.cost) {
        return b.cost - a.cost;
      }
      return comparePath(a.path, b.path);
    });
    const toRename = members.slice(1).sort((a: KeyedModule, b: KeyedModule) => {
      if (a.cost !== b.cost) {
        return a.cost - b.cost;
      }
      return comparePath(a.path, b.path);
    });
    for (const member of toRename) {
      const oldCount = occupancy.get(member.key) ?? 0;
      if (oldCount <= 1) {
        occupancy.delete(member.key);
      } else {
        occupancy.set(member.key, oldCount - 1);
      }
      const candidate = findFreeName(member.path, rules, occupancy);
      const candidateKey = canonicalKey(candidate, rules);
      occupancy.set(candidateKey, 1);
      finalPathOf.set(member.path, candidate);
      finalKeyOf.set(member.path, candidateKey);
      renames.push({ from: member.path, to: candidate, cost: member.cost });
      totalCost += member.cost;
    }
  }
  return { renames, totalCost, finalPathOf, finalKeyOf };
}
