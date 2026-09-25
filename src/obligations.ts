/**
 * Obligation model. Every obligation carries a stable `id` so merge results
 * can cite provenance evidence back to the exact obligations that produced
 * each value.
 */

export type LimitRelation = {
  left: string;
  rel: '<=' | '>=' | '==';
  right: string;
  /** constant offset k in `left <rel> right + k`; defaults to 0 */
  offset?: number;
};

export type LimitObligation = {
  kind: 'limit';
  id: string;
  /** limit variable name, e.g. "dailyAmount" */
  name: string;
  /** upper = cap (stricter means smaller); lower = floor (stricter means larger) */
  bound: 'upper' | 'lower';
  value: number;
  /** dependency relations between limit variables this obligation imposes */
  requires?: readonly LimitRelation[];
};

export type MaskObligation = {
  kind: 'mask';
  id: string;
  fields: readonly string[];
};

/** Order-independent: the merged tag set must not depend on input order. */
export type AuditTagsObligation = {
  kind: 'auditTags';
  id: string;
  tags: readonly string[];
};

/** Order-sensitive: steps[i] must be executed before steps[i+1]. */
export type OrderedStepsObligation = {
  kind: 'orderedSteps';
  id: string;
  steps: readonly string[];
};

/** Choice among a finite set of output formats; merge intersects choices. */
export type OutputFormatObligation = {
  kind: 'outputFormat';
  id: string;
  allowed: readonly string[];
};

export type Obligation =
  | LimitObligation
  | MaskObligation
  | AuditTagsObligation
  | OrderedStepsObligation
  | OutputFormatObligation;

export type ObligationKind = Obligation['kind'];

export function obligationId(ob: Obligation): string {
  return ob.id;
}
