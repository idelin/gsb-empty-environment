export type Value = number;

/** 二元约束：作用于变量 a 与 b（均为下标），holds(va, vb) 为真表示相容。 */
export interface BinaryConstraint {
  readonly a: number;
  readonly b: number;
  readonly name: string;
  readonly holds: (va: Value, vb: Value) => boolean;
}

export interface CspProblem {
  readonly varNames: string[];
  readonly domains: Value[][];
  readonly constraints: BinaryConstraint[];
}

/** 校验一份完整赋值是否满足所有约束。 */
export function isSatisfied(problem: CspProblem, assignment: Value[]): boolean {
  return problem.constraints.every((c) =>
    c.holds(assignment[c.a], assignment[c.b]),
  );
}
