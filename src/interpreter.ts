/**
 * Slow reference interpreter: a straightforward, unbudgeted evaluator used
 * to cross-check the compiled plan in differential tests. Shares the safe
 * access layer and the pure filter semantics with the compiled engine.
 */
import type { PathAst, Segment } from './ast.ts';
import { AccessDeniedError } from './errors.ts';
import { getOwn, isDataContainer, ownKeys } from './safeAccess.ts';
import { evalFilterSync } from './semantics.ts';
import type { Match } from './compiler.ts';

function childKey(container: unknown, key: string): string | number {
  return Array.isArray(container) ? Number(key) : key;
}

interface DescentEntry {
  value: unknown;
  path: (string | number)[];
  parent: DescentEntry | null;
}

function isAncestor(value: unknown, entry: DescentEntry | null): boolean {
  for (let e = entry; e !== null; e = e.parent) {
    if (e.value === value) return true;
  }
  return false;
}

function walk(
  segments: Segment[],
  index: number,
  value: unknown,
  path: (string | number)[],
  matches: Match[],
  expression: string,
): void {
  if (index >= segments.length) {
    matches.push({ path: Object.freeze(path.slice()), value });
    return;
  }
  const seg = segments[index];
  switch (seg.type) {
    case 'child': {
      const r = getOwn(value, seg.key);
      if (r.kind === 'denied') {
        throw new AccessDeniedError(`access to property ${JSON.stringify(seg.key)} is denied by policy`, expression);
      }
      if (r.kind === 'value') walk(segments, index + 1, r.value, [...path, seg.key], matches, expression);
      return;
    }
    case 'index': {
      const r = getOwn(value, String(seg.index));
      if (r.kind === 'value') walk(segments, index + 1, r.value, [...path, seg.index], matches, expression);
      return;
    }
    case 'wildcard':
    case 'filter': {
      if (!isDataContainer(value)) return;
      for (const key of ownKeys(value)) {
        const r = getOwn(value, key);
        if (r.kind !== 'value') continue;
        if (seg.type === 'filter' && !evalFilterSync(seg.expr, r.value)) continue;
        walk(segments, index + 1, r.value, [...path, childKey(value, key)], matches, expression);
      }
      return;
    }
    case 'descent': {
      const root: DescentEntry = { value, path: path.slice(), parent: null };
      const stack: DescentEntry[] = [root];
      while (stack.length > 0) {
        const entry = stack.pop() as DescentEntry;
        const hit = getOwn(entry.value, seg.key);
        if (hit.kind === 'value') {
          walk(segments, index + 1, hit.value, [...entry.path, seg.key], matches, expression);
        }
        if (!isDataContainer(entry.value)) continue;
        for (const key of ownKeys(entry.value)) {
          const child = getOwn(entry.value, key);
          if (child.kind !== 'value' || !isDataContainer(child.value)) continue;
          if (isAncestor(child.value, entry)) continue;
          stack.push({
            value: child.value,
            path: [...entry.path, childKey(entry.value, key)],
            parent: entry,
          });
        }
      }
      return;
    }
  }
}

/** Evaluate an AST directly, without any budget. */
export function evaluate(ast: PathAst, root: unknown, expression = '<ast>'): Match[] {
  const matches: Match[] = [];
  walk(ast.segments, 0, root, [], matches, expression);
  return matches;
}
