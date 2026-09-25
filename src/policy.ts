import type { Condition } from './conditions.ts';
import type { CombiningAlgorithm } from './combiners.ts';
import type { Decision, Obligation } from './types.ts';

export interface PolicyRule {
  id: string;
  effect: Extract<Decision, 'Permit' | 'Deny'>;
  condition?: Condition;
  obligations?: Obligation[];
}

export interface PolicyModule {
  id: string;
  combining: CombiningAlgorithm;
  rules: PolicyRule[];
  moduleRefs?: string[];
}

export interface ModuleSet {
  modules: Record<string, PolicyModule>;
  root: string;
}
