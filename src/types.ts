export interface MutexConstraint {
  type: "mutex";
  a: number;
  b: number;
}

export interface ImpliesConstraint {
  type: "implies";
  from: number;
  to: number;
}

export type Constraint = MutexConstraint | ImpliesConstraint;

export interface Problem {
  featureNames: string[];
  constraints: Constraint[];
}

export function formatConstraint(c: Constraint, names: string[]): string {
  if (c.type === "mutex") {
    return `mutex(${names[c.a]}, ${names[c.b]})`;
  }
  return `${names[c.from]} => ${names[c.to]}`;
}
