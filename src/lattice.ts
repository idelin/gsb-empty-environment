export interface Lattice<T> {
  readonly bottom: T;
  leq(a: T, b: T): boolean;
  join(a: T, b: T): T;
  equals(a: T, b: T): boolean;
  elements(): readonly T[];
  show?(value: T): string;
}

export const booleanLattice: Lattice<boolean> = {
  bottom: false,
  leq: (a, b) => !a || b,
  join: (a, b) => a || b,
  equals: (a, b) => a === b,
  elements: () => [false, true],
  show: (value) => (value ? "true" : "false"),
};

export function powerSetLattice<T>(universe: readonly T[]): Lattice<ReadonlySet<T>> {
  return {
    bottom: new Set<T>(),
    leq: (a, b) => {
      for (const item of a) if (!b.has(item)) return false;
      return true;
    },
    join: (a, b) => new Set<T>([...a, ...b]),
    equals: (a, b) => a.size === b.size && [...a].every((item) => b.has(item)),
    elements: () => {
      const subsets: Set<T>[] = [new Set<T>()];
      for (const item of universe) {
        const count = subsets.length;
        for (let i = 0; i < count; i++) subsets.push(new Set<T>([...subsets[i], item]));
      }
      return subsets;
    },
    show: (value) => `{${[...value].map(String).join(",")}}`,
  };
}
