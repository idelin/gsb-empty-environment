export type Decision = 'Permit' | 'Deny' | 'NotApplicable' | 'Indeterminate';

export type AttributeValue = string | number | boolean;
export type AttributeBag = Readonly<Record<string, AttributeValue>>;

export interface QuotaObligation {
  kind: 'quota';
  id: string;
  limit: number;
}

export interface MaskObligation {
  kind: 'mask';
  id: string;
  field: string;
  format: string;
}

export type Obligation = QuotaObligation | MaskObligation;

export interface ObligationConflict {
  reason: string;
  left?: Obligation;
  right?: Obligation;
}

export interface EvaluationResult {
  decision: Decision;
  obligations: Obligation[];
  conflicts: ObligationConflict[];
}

export const permit = (obligations: Obligation[] = []): EvaluationResult => ({
  decision: 'Permit',
  obligations,
  conflicts: [],
});

export const deny = (): EvaluationResult => ({
  decision: 'Deny',
  obligations: [],
  conflicts: [],
});

export const notApplicable = (): EvaluationResult => ({
  decision: 'NotApplicable',
  obligations: [],
  conflicts: [],
});

export const indeterminate = (conflicts: ObligationConflict[] = []): EvaluationResult => ({
  decision: 'Indeterminate',
  obligations: [],
  conflicts,
});
