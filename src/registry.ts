// Versioned rule registry.
//
// - Publishing validates the rule set first; an invalid set is rejected
//   and the previously published version stays active, so a bad publish
//   can never corrupt results produced by the last good version.
// - A local rule update recomputes only the fields in the dependency
//   closure of the changed rules: their old effects are reverted via the
//   stored inverse patches and the affected rules are re-run in order.

import type { JsonValue } from "./json.ts";
import {
  applyPatchOps,
  applyRules,
  buildInverse,
} from "./normalize.ts";
import type { NormalizeResult } from "./normalize.ts";
import { analyzeRules, dependencyClosure } from "./deps.ts";
import type { RuleIssue } from "./deps.ts";
import { ruleWrites } from "./rules.ts";
import type { PathPattern, Rule } from "./rules.ts";

export interface RuleSetVersion {
  version: number;
  rules: Rule[];
}

export type PublishOutcome =
  | { ok: true; version: number }
  | { ok: false; issues: RuleIssue[]; activeVersion: number | null };

export type UpdateOutcome =
  | { ok: true; version: number; affectedRuleIds: string[] }
  | { ok: false; issues: RuleIssue[]; activeVersion: number | null };

export interface VersionedResult extends NormalizeResult {
  version: number;
}

export class RuleRegistry {
  private versions: RuleSetVersion[] = [];

  activeVersion(): number | null {
    return this.versions.length === 0 ? null : this.versions[this.versions.length - 1].version;
  }

  activeRules(): Rule[] {
    return this.versions.length === 0
      ? []
      : this.versions[this.versions.length - 1].rules;
  }

  /** Validates and publishes a full rule set. Invalid sets are rejected;
   *  the previous active version is left untouched. */
  publish(rules: Rule[]): PublishOutcome {
    const { issues } = analyzeRules(rules);
    if (issues.length > 0) {
      return { ok: false, issues, activeVersion: this.activeVersion() };
    }
    const version = this.versions.length + 1;
    this.versions.push({ version, rules: rules.map((r) => ({ ...r })) });
    return { ok: true, version };
  }

  normalize(tree: JsonValue): VersionedResult {
    const version = this.activeVersion();
    if (version === null) throw new Error("no published rule set");
    return { ...applyRules(tree, this.activeRules()), version };
  }

  /** Replaces one rule and publishes the result as a new version.
   *  The affected-rule set is computed against the union of the old and
   *  new write paths, so fields the rule no longer touches are still
   *  reverted correctly. */
  updateRule(id: string, replacement: Rule): UpdateOutcome {
    const current = this.activeRules();
    const existing = current.find((r) => r.id === id);
    if (!existing) {
      return {
        ok: false,
        issues: [{ type: "alias-conflict", ruleIds: [id], message: `unknown rule "${id}"` }],
        activeVersion: this.activeVersion(),
      };
    }
    const next = current.map((r) => (r.id === id ? replacement : r));
    const { issues } = analyzeRules(next);
    if (issues.length > 0) {
      return { ok: false, issues, activeVersion: this.activeVersion() };
    }
    const version = this.versions.length + 1;
    this.versions.push({ version, rules: next.map((r) => ({ ...r })) });

    const unionWrites: PathPattern[] = [...ruleWrites(existing), ...ruleWrites(replacement)];
    const closureRules = next.map((r) =>
      r.id === id ? ({ ...r, writes: unionWrites, targets: unionWrites } as Rule) : r,
    );
    return { ok: true, version, affectedRuleIds: dependencyClosure(closureRules, [id]) };
  }

  /** Incremental recompute: reverts the old effects of the affected
   *  rules using the stored inverse patches, then re-runs only those
   *  rules in pipeline order. All other fields keep their values and
   *  their change records from the previous run. */
  recompute(
    prev: NormalizeResult,
    affectedRuleIds: readonly string[],
  ): NormalizeResult {
    const affected = new Set(affectedRuleIds);
    const revertOps = prev.inverse.filter((op) => affected.has(op.ruleId));
    const base = applyPatchOps(prev.output, revertOps);

    const affectedRules = this.activeRules().filter((r) => affected.has(r.id));
    const fresh = applyRules(base, affectedRules);

    const keptChanges = prev.changes.filter((c) => !affected.has(c.ruleId));
    const keptErrors = prev.errors.filter((e) => !affected.has(e.ruleId));
    const changes = [...keptChanges, ...fresh.changes].map((c, i) => ({ ...c, seq: i }));
    const errors = [...keptErrors, ...fresh.errors];
    return { output: fresh.output, changes, errors, inverse: buildInverse(changes) };
  }
}
