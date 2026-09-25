import { evaluateCondition, MissingAttributeError, type Condition } from './conditions.ts';
import { combine } from './combiners.ts';
import type { ModuleSet, PolicyModule } from './policy.ts';
import type { ModuleRegistry } from './registry.ts';
import type { AttributeBag, EvaluationResult, Obligation } from './types.ts';

export interface DecisionRecord {
  id: string;
  version: number;
  attributes: AttributeBag;
  result: EvaluationResult;
}

export interface ReplayOutcome {
  record: DecisionRecord;
  replayed: EvaluationResult;
  echoedAttributes: AttributeBag;
  version: number;
}

export class EvidenceMissingError extends Error {
  readonly decisionId: string;

  constructor(decisionId: string) {
    super(`interpretation evidence missing for decision: ${decisionId}`);
    this.name = 'EvidenceMissingError';
    this.decisionId = decisionId;
  }
}

export class PolicyEngine {
  private readonly registry: ModuleRegistry;
  private log = new Map<string, DecisionRecord>();
  private seq = 0;

  constructor(registry: ModuleRegistry) {
    this.registry = registry;
  }

  evaluate(attrs: AttributeBag): EvaluationResult {
    const result = this.evaluateModule(this.registry.current.root, this.registry.current, attrs, []);
    const id = `d${++this.seq}`;
    this.log.set(id, { id, version: this.registry.currentVersion, attributes: { ...attrs }, result });
    return result;
  }

  lastDecisionId(): string {
    return `d${this.seq}`;
  }

  replay(decisionId: string): ReplayOutcome {
    const record = this.log.get(decisionId);
    if (!record) throw new EvidenceMissingError(decisionId);
    const moduleSet = this.registry.at(record.version);
    const replayed = this.evaluateModule(moduleSet.root, moduleSet, record.attributes, []);
    return { record, replayed, echoedAttributes: { ...record.attributes }, version: record.version };
  }

  dropEvidence(decisionId: string): void {
    this.log.delete(decisionId);
  }

  private evaluateModule(id: string, ms: ModuleSet, attrs: AttributeBag, stack: string[]): EvaluationResult {
    if (stack.includes(id)) {
      return {
        decision: 'Indeterminate',
        obligations: [],
        conflicts: [{ reason: `module ring at runtime: ${[...stack, id].join(' -> ')}` }],
      };
    }
    const mod: PolicyModule = ms.modules[id];
    const nextStack = [...stack, id];
    const results: EvaluationResult[] = [];
    for (const rule of mod.rules) {
      results.push(this.evaluateRule(rule.condition, rule.effect, rule.obligations ?? [], attrs));
    }
    for (const ref of mod.moduleRefs ?? []) {
      results.push(this.evaluateModule(ref, ms, attrs, nextStack));
    }
    return combine(mod.combining, results);
  }

  private evaluateRule(
    condition: Condition | undefined,
    effect: 'Permit' | 'Deny',
    obligations: Obligation[],
    attrs: AttributeBag,
  ): EvaluationResult {
    if (!condition) {
      return effect === 'Permit'
        ? { decision: 'Permit', obligations, conflicts: [] }
        : { decision: 'Deny', obligations: [], conflicts: [] };
    }
    let matched: boolean;
    try {
      matched = evaluateCondition(condition, attrs);
    } catch (err) {
      if (err instanceof MissingAttributeError) {
        return { decision: 'Indeterminate', obligations: [], conflicts: [] };
      }
      throw err;
    }
    if (!matched) return { decision: 'NotApplicable', obligations: [], conflicts: [] };
    return effect === 'Permit'
      ? { decision: 'Permit', obligations, conflicts: [] }
      : { decision: 'Deny', obligations: [], conflicts: [] };
  }
}
