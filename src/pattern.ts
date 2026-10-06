import { Atom, isAtom } from './tree';

/**
 * A pattern over rule trees.
 * - `lit`  matches one atom equal to `value`.
 * - `var`  matches any single node (atom or list) and binds `name` to it.
 * - `list` matches a list node item by item; the last item may be a `tail`.
 */
export type Pattern =
  | { kind: 'lit'; value: Atom }
  | { kind: 'var'; name: string }
  | { kind: 'list'; items: Item[] };

/** A list item: a nested pattern, or a tail variable (last item only). */
export type Item = Pattern | { kind: 'tail'; name: string };

export const lit = (value: Atom): Pattern => ({ kind: 'lit', value });
export const v = (name: string): Pattern => ({ kind: 'var', name });
export const tail = (name: string): Item => ({ kind: 'tail', name });
export const list = (...items: Item[]): Pattern =>
  validatePattern({ kind: 'list', items });

/**
 * Checks pattern well-formedness and returns the pattern unchanged.
 * Throws when:
 * - a literal is not an atom,
 * - a variable name is empty,
 * - a tail variable is not the last item of its list,
 * - one name is used both as a node variable and as a tail variable.
 */
export function validatePattern(pattern: Pattern): Pattern {
  const nameKinds = new Map<string, 'var' | 'tail'>();
  const noteName = (name: string, kind: 'var' | 'tail'): void => {
    if (name.length === 0) throw new Error('variable name must not be empty');
    const prev = nameKinds.get(name);
    if (prev !== undefined && prev !== kind) {
      throw new Error(`name "${name}" used as both ${prev} and tail variable`);
    }
    nameKinds.set(name, kind);
  };
  const walk = (p: Pattern): void => {
    switch (p.kind) {
      case 'lit':
        if (!isAtom(p.value)) throw new Error('literal must be an atom');
        return;
      case 'var':
        noteName(p.name, 'var');
        return;
      case 'list':
        p.items.forEach((item, i) => {
          if (item.kind === 'tail') {
            if (i !== p.items.length - 1) {
              throw new Error('tail variable is only allowed as the last list item');
            }
            noteName(item.name, 'tail');
          } else {
            walk(item);
          }
        });
    }
  };
  walk(pattern);
  return pattern;
}

/** Distinct variable names in first-occurrence (depth-first, left-to-right) order. */
export function patternNames(pattern: Pattern): string[] {
  const names: string[] = [];
  const seen = new Set<string>();
  const note = (name: string): void => {
    if (!seen.has(name)) {
      seen.add(name);
      names.push(name);
    }
  };
  const walk = (p: Pattern): void => {
    switch (p.kind) {
      case 'lit':
        return;
      case 'var':
        note(p.name);
        return;
      case 'list':
        for (const item of p.items) {
          if (item.kind === 'tail') note(item.name);
          else walk(item);
        }
    }
  };
  walk(pattern);
  return names;
}

/**
 * All occurrence paths of every variable, in first-occurrence order.
 * Because tail variables may only close a list, fixed items line up with the
 * same indices in the subject, so each path also addresses the subject node
 * (for `var`) or the subject slice start (for `tail`) that the name binds.
 */
export function occurrencePaths(
  pattern: Pattern,
): Array<{ name: string; kind: 'var' | 'tail'; path: number[] }> {
  const out: Array<{ name: string; kind: 'var' | 'tail'; path: number[] }> = [];
  const walk = (p: Pattern, path: number[]): void => {
    switch (p.kind) {
      case 'lit':
        return;
      case 'var':
        out.push({ name: p.name, kind: 'var', path });
        return;
      case 'list':
        p.items.forEach((item, i) => {
          if (item.kind === 'tail') {
            out.push({ name: item.name, kind: 'tail', path: [...path, i] });
          } else {
            walk(item, [...path, i]);
          }
        });
    }
  };
  walk(pattern, []);
  return out;
}
