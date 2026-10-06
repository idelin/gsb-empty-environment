import type { CstFile } from "./parser.ts";

/** Semantic (block-structure) view of a document. */
export interface SemNode {
  key: string;
  /** Value text for `key: value` entries, null for block openers. */
  value: string | null;
  children: SemNode[];
}

export function semantics(file: CstFile): SemNode[] {
  const build = (idx: number): SemNode => {
    const line = file.lines[idx]!;
    return {
      key: line.key,
      value: line.hasValue ? line.value : null,
      children: line.children.map(build),
    };
  };
  return file.lines
    .filter((l) => l.kind === "entry" && l.depth === 0)
    .map((l) => build(l.index));
}

export function findPath(nodes: SemNode[], path: string[]): SemNode | null {
  let level = nodes;
  let found: SemNode | null = null;
  for (const key of path) {
    found = level.find((n) => n.key === key) ?? null;
    if (found === null) return null;
    level = found.children;
  }
  return found;
}
