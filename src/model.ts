/**
 * Domain model.
 *
 * The policy language is deliberately closed: it may only read explicitly
 * declared attributes, and only through finite-set membership, integer
 * comparisons and boolean combinators. No external identity services.
 */

export type Scalar = number | string | boolean;
export type Value = Scalar | string[];
export type Input = Record<string, Value>;

export type Cmp = '==' | '!=' | '<' | '<=' | '>' | '>=';

/** Boolean condition tree over explicit attributes. */
export type Cond =
  | { op: 'int'; attr: string; cmp: Cmp; value: number }
  | { op: 'eq'; attr: string; value: Scalar }
  | { op: 'contains'; attr: string; value: string }
  | { op: 'present'; attr: string }
  | { op: 'and'; args: Cond[] }
  | { op: 'or'; args: Cond[] }
  | { op: 'not'; arg: Cond };

/** Field dependency: attribute may only be present when `dep.attr === dep.equals`. */
export interface Dep {
  attr: string;
  equals: Scalar;
}

export type AttrSpec =
  | { kind: 'int'; min: number; max: number; required?: boolean; dependsOn?: Dep }
  | { kind: 'enum'; values: readonly string[]; required?: boolean; dependsOn?: Dep }
  | { kind: 'set'; members: readonly string[]; minSize?: number; required?: boolean; dependsOn?: Dep }
  | { kind: 'bool'; required?: boolean; dependsOn?: Dep };

export interface Schema {
  attrs: Record<string, AttrSpec>;
  /** Global validity constraints (integer conditions / finite sets / boolean ops). */
  constraints?: Cond[];
}

/** A versioned judgment rule: verdict is `true` (allow) iff `cond` holds. */
export interface Policy {
  version: string;
  cond: Cond;
}

export interface RuleVersions {
  a: string;
  b: string;
}
