import type { Heading, LevelViolation } from './types.ts';

export interface NumberingResult {
  numbers: Map<string, string | null>;
  violations: LevelViolation[];
}

export function subtreeEnd(headings: Heading[], start: number): number {
  const level = headings[start].level;
  let end = start + 1;
  while (end < headings.length && headings[end].level > level) end++;
  return end;
}

function parentLevel(headings: Heading[], index: number): number {
  const level = headings[index].level;
  for (let i = index - 1; i >= 0; i--) {
    if (headings[i].level < level) return headings[i].level;
  }
  return 0;
}

export function computeNumbering(headings: Heading[]): NumberingResult {
  const numbers = new Map<string, string | null>();
  const violations: LevelViolation[] = [];
  const counters: number[] = [];
  let currentDepth = 0;

  headings.forEach((heading, index) => {
    const maxAllowed = Math.max(1, parentLevel(headings, index) + 1);
    const isViolation =
      !Number.isInteger(heading.level) || heading.level < 1 || heading.level > maxAllowed;
    if (isViolation) {
      const end = subtreeEnd(headings, index);
      const rangeIds = headings.slice(index, end).map((h) => h.id);
      violations.push({
        headingId: heading.id,
        title: heading.title,
        actualLevel: heading.level,
        maxAllowedLevel: maxAllowed,
        fixRange: {
          startId: heading.id,
          endId: rangeIds[rangeIds.length - 1],
          headingIds: rangeIds,
        },
        suggestion: { kind: 'shift-subtree', delta: maxAllowed - heading.level },
        alternative: { kind: 'insert-ancestors', count: Math.max(0, heading.level - maxAllowed) },
      });
    }
    if (isViolation || heading.level > currentDepth + 1) {
      numbers.set(heading.id, null);
      return;
    }
    counters.length = heading.level;
    counters[heading.level - 1] = (counters[heading.level - 1] ?? 0) + 1;
    currentDepth = heading.level;
    numbers.set(heading.id, counters.slice(0, heading.level).join('.'));
  });

  return { numbers, violations };
}
