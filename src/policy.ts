import { evaluateCondition, type AttributeDescriptor, type AttributeValue, type Condition } from './condition.ts';
import { combine, type BranchOutcome, type CombinedOutcome, type CombinerName } from './combiners.ts';
import type { Obligation } from './obligations.ts';

export interface Rule {
  readonly id: string;
  readonly effect: 'Permit' | 'Deny';
  readonly condition: Condition;
  readonly obligations?: readonly Obligation[];
}

export interface Policy {
  readonly id: string;
  readonly combiner: CombinerName;
  readonly rules: readonly Rule[];
}

export function evaluatePolicy(
  policy: Policy,
  attributes: Readonly<Record<string, AttributeValue>>,
  descriptors: ReadonlyMap<string, AttributeDescriptor>,
): CombinedOutcome {
  const branches: BranchOutcome[] = policy.rules.map((rule) => {
    const source = `${policy.id}/${rule.id}`;
    const result = evaluateCondition(rule.condition, attributes, descriptors);
    if (!result.ok) {
      return {
        source,
        decision: 'Indeterminate',
        obligations: [],
        missingEvidence: result.failure.missingEvidence,
        missingValues: result.failure.missingValues,
        typeErrors: result.failure.typeErrors,
        accessed: result.accessed,
      };
    }
    return {
      source,
      decision: result.value ? rule.effect : 'NotApplicable',
      obligations: result.value ? (rule.obligations ?? []) : [],
      missingEvidence: [],
      missingValues: [],
      typeErrors: [],
      accessed: result.accessed,
    };
  });
  return combine(policy.combiner, branches);
}
