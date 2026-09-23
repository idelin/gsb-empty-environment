/**
 * 有限格（finite lattice）：不动点迭代的值域。
 * 所有节点的值只允许沿格序 `leq` 单调上升，`join` 给出最小上界。
 */
export interface Lattice<T> {
  readonly bottom: T;
  /** 偏序：a <= b */
  leq(a: T, b: T): boolean;
  /** 最小上界（并） */
  join(a: T, b: T): T;
  /** 值相等 */
  eq(a: T, b: T): boolean;
}

/** 布尔格：false <= true，join 即 OR。可达性闭包用它。 */
export const booleanLattice: Lattice<boolean> = {
  bottom: false,
  leq: (a, b) => !a || b,
  join: (a, b) => a || b,
  eq: (a, b) => a === b,
};

/** 有限幂集格：子集序，join 即并集。 */
export function setLattice<T>(): Lattice<ReadonlySet<T>> {
  return {
    bottom: new Set<T>(),
    leq: (a, b) => [...a].every((x) => b.has(x)),
    join: (a, b) => new Set<T>([...a, ...b]),
    eq: (a, b) => a.size === b.size && [...a].every((x) => b.has(x)),
  };
}
