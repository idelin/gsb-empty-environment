import type { ConflictGroup } from "./types.js";

export interface KeyedModule {
  path: string;
  key: string;
  cost: number;
}

export function findConflicts(modules: KeyedModule[]): ConflictGroup[] {
  const byKey: Map<string, string[]> = new Map();
  for (const m of modules) {
    const existing = byKey.get(m.key);
    if (existing) {
      existing.push(m.path);
    } else {
      byKey.set(m.key, [m.path]);
    }
  }
  const groups: ConflictGroup[] = [];
  const keys = Array.from(byKey.keys()).sort();
  for (const key of keys) {
    const members = byKey.get(key);
    if (members && members.length > 1) {
      groups.push({ key, modules: members.slice().sort() });
    }
  }
  return groups;
}
