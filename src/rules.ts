/**
 * The implemented schema subset. Nothing beyond these five rule kinds is
 * promised; anything else is rejected up front with an explicit error.
 */

export type Operand =
  | { kind: 'field'; path: string }
  | { kind: 'literal'; value: number | string | boolean };

export const field = (path: string): Operand => ({ kind: 'field', path });
export const literal = (value: number | string | boolean): Operand => ({ kind: 'literal', value });

export type CompareOp = '==' | '!=' | '<' | '<=' | '>' | '>=';

export type Condition =
  | { kind: 'compare'; op: CompareOp; left: Operand; right: Operand }
  | { kind: 'exists'; path: string }
  | { kind: 'and'; all: Condition[] }
  | { kind: 'or'; any: Condition[] }
  | { kind: 'not'; cond: Condition };

export type Rule =
  | { kind: 'equals'; left: Operand; right: Operand }
  | { kind: 'compare'; op: CompareOp; left: Operand; right: Operand }
  | { kind: 'requiredIf'; when: Condition; path: string }
  | { kind: 'sum'; listPath: string; itemPath?: string; op: '==' | '<=' | '>='; limit: number }
  | { kind: 'typeIs'; path: string; type: 'number' | 'string' | 'boolean' | 'list' | 'object' };

export const RULE_KINDS = ['equals', 'compare', 'requiredIf', 'sum', 'typeIs'] as const;

export function isSupportedRule(rule: unknown): rule is Rule {
  return (
    typeof rule === 'object' &&
    rule !== null &&
    RULE_KINDS.includes((rule as { kind?: string }).kind as never)
  );
}
