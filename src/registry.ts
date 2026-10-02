import type { RuleSet } from './types.ts';

export class RuleRegistry {
  private sets: RuleSet[] = [];

  publish(ruleSet: RuleSet): void {
    const existing = this.sets.findIndex((s) => s.version === ruleSet.version);
    if (existing >= 0) {
      throw new Error(`rule version already published: ${ruleSet.version}`);
    }
    this.sets.push(ruleSet);
    this.sets.sort((a, b) => a.effectiveFrom - b.effectiveFrom);
  }

  activeAt(timestamp: number): RuleSet {
    for (let i = this.sets.length - 1; i >= 0; i -= 1) {
      if (this.sets[i].effectiveFrom <= timestamp) {
        return this.sets[i];
      }
    }
    throw new Error(`no active rule set at t=${timestamp}`);
  }

  current(): RuleSet {
    if (this.sets.length === 0) {
      throw new Error('no rule sets published');
    }
    return this.sets[this.sets.length - 1];
  }

  get(version: string): RuleSet {
    const found = this.sets.find((s) => s.version === version);
    if (!found) {
      throw new Error(`rule version not found: ${version}`);
    }
    return found;
  }

  versions(): string[] {
    return this.sets.map((s) => s.version);
  }
}
