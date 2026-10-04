import { Schema } from './model';

export interface FdPreservation {
  fdIndex: number;
  preserved: boolean;
  closureTrace: number[][];
  reachableClosure: number[];
  gap: number[];
}

export interface PreservationResult {
  preserved: boolean;
  perFd: FdPreservation[];
}

export function attributeClosure(schema: Schema, seed: ReadonlySet<number>): Set<number> {
  const closure = new Set<number>(seed);
  let changed = true;
  while (changed) {
    changed = false;
    for (const fd of schema.fds) {
      if (fd.lhs.every((attr) => closure.has(attr))) {
        for (const attr of fd.rhs) {
          if (!closure.has(attr)) {
            closure.add(attr);
            changed = true;
          }
        }
      }
    }
  }
  return closure;
}

function sortedSet(values: ReadonlySet<number>): number[] {
  return Array.from(values).sort((a, b) => a - b);
}

export function checkPreservation(schema: Schema): PreservationResult {
  const perFd: FdPreservation[] = schema.fds.map((fd, fdIndex) => {
    const reachable = new Set<number>(fd.lhs);
    const closureTrace: number[][] = [sortedSet(reachable)];
    let changed = true;
    while (changed) {
      changed = false;
      for (const member of schema.decomposition) {
        const inside = new Set<number>();
        for (const attr of reachable) {
          if (member.includes(attr)) {
            inside.add(attr);
          }
        }
        if (inside.size === 0) {
          continue;
        }
        const closure = attributeClosure(schema, inside);
        for (const attr of closure) {
          if (member.includes(attr) && !reachable.has(attr)) {
            reachable.add(attr);
            changed = true;
          }
        }
      }
      if (changed) {
        closureTrace.push(sortedSet(reachable));
      }
    }
    const gap = fd.rhs.filter((attr) => !reachable.has(attr));
    return {
      fdIndex,
      preserved: gap.length === 0,
      closureTrace,
      reachableClosure: sortedSet(reachable),
      gap,
    };
  });
  return { preserved: perFd.every((entry) => entry.preserved), perFd };
}
