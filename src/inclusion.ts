import { rangeContains } from "./deployment.ts";
import { ctorId } from "./canonical.ts";
import type {
  CanonicalGrammar,
  CanonicalProduction,
  ConstructorDescriptor,
  InclusionCertificate,
  InclusionObligation,
  VersionRange,
  WideningWitness,
} from "./types.ts";

export interface InclusionInput {
  tenant: string;
  leftTableId: string;
  rightTableId: string;
  left: CanonicalGrammar;
  right: CanonicalGrammar;
  /** ctorId -> declared new-table version range, when one was configured. */
  ranges: Map<string, VersionRange>;
  budget: number;
  issuedAt: string;
  rulesVersion: string;
}

export interface InclusionRun {
  certificate: InclusionCertificate;
  exhaustedBudget: boolean;
}

function key(obligation: InclusionObligation): string {
  return `${obligation.direction}:${obligation.left}<=${obligation.right}`;
}

/**
 * Coinductive regular-tree-language inclusion.
 *
 * For every obligation L ? R, each L-production must be matched by an
 * R-production on the same constructor. Slot variance is read from the
 * certified (old/right) catalog: `out` keeps direction, `in` flips it,
 * `invariant` demands both directions. An obligation already in the
 * coinductive seen-set closes immediately (greatest fixpoint). Everything is
 * iterative and every step is charged against the configured budget.
 */
export function decideInclusion(input: InclusionInput): InclusionRun {
  const { left, right } = input;
  const seen = new Set<string>();
  const proof: InclusionObligation[] = [];
  const witnesses: WideningWitness[] = [];
  const work: InclusionObligation[] = [
    { left: left.start, right: right.start, direction: "forward" },
  ];
  let spent = 0;
  let exhaustedBudget = false;

  while (work.length > 0) {
    if (spent >= input.budget) {
      exhaustedBudget = true;
      break;
    }
    spent++;
    const obligation = work.pop()!;
    const obligationKey = key(obligation);
    if (seen.has(obligationKey)) {
      continue;
    }

    const leftProductions = ordered(left, obligation.left);
    const rightProductions = ordered(right, obligation.right);
    if (leftProductions.length === 0) {
      seen.add(obligationKey);
      proof.push(obligation);
      continue;
    }
    if (rightProductions.length === 0) {
      witnesses.push({
        kind: "empty-language",
        nonterminal: obligation.right,
        detail: `right nonterminal accepts no terms while ${obligation.left} requires terms`,
      });
      break;
    }

    let failed = false;
    for (const production of leftProductions) {
      const match = rightProductions.find(
        (candidate) => candidate.ctorId === production.ctorId,
      );
      if (!match) {
        witnesses.push({
          kind: "unmatched-production",
          ctorId: production.ctorId,
          nonterminal: obligation.left,
          detail: `constructor ${production.ctorId} is not accepted at ${obligation.right}`,
        });
        failed = true;
        break;
      }
      const coverage = checkVersionCoverage(
        left.catalog.get(production.ctorId),
        right.catalog.get(match.ctorId),
        input.ranges,
        obligation,
      );
      if (coverage) {
        witnesses.push(coverage);
        failed = true;
        break;
      }
      const descriptor = right.catalog.get(match.ctorId);
      if (!descriptor) {
        witnesses.push({
          kind: "unmatched-production",
          ctorId: match.ctorId,
          detail: "right catalog missing constructor descriptor",
        });
        failed = true;
        break;
      }
      const next = childObligations(production, match, descriptor.variances, obligation.direction);
      for (const child of next) {
        if (!seen.has(key(child))) {
          work.push(child);
        }
      }
    }
    if (failed) {
      break;
    }
    seen.add(obligationKey);
    proof.push(obligation);
  }

  const verdict = exhaustedBudget
    ? "insufficient"
    : witnesses.length > 0
      ? "widened"
      : "subset";
  const startPair: InclusionObligation = {
    left: left.start,
    right: right.start,
    direction: "forward",
  };
  const certificate: InclusionCertificate = {
    rulesVersion: input.rulesVersion,
    verdict,
    tenant: input.tenant,
    leftTableId: input.leftTableId,
    rightTableId: input.rightTableId,
    leftGrammarDigest: left.digest,
    rightGrammarDigest: right.digest,
    startPair,
    proof,
    witnesses,
    budget: input.budget,
    spent,
    issuedAt: input.issuedAt,
    digest: "",
  };
  return { certificate, exhaustedBudget };
}

function ordered(
  grammar: CanonicalGrammar,
  nonterminal: string,
): CanonicalProduction[] {
  return grammar.nonterminals.get(nonterminal) ?? [];
}

function childObligations(
  leftProduction: CanonicalProduction,
  rightProduction: CanonicalProduction,
  variances: Array<"in" | "out" | "invariant">,
  direction: "forward" | "backward",
): InclusionObligation[] {
  const obligations: InclusionObligation[] = [];
  for (let slot = 0; slot < variances.length; slot++) {
    const leftChild = leftProduction.children[slot];
    const rightChild = rightProduction.children[slot];
    const variance = variances[slot];
    if (variance === "out" || variance === "invariant") {
      obligations.push({ left: leftChild, right: rightChild, direction });
    }
    if (variance === "in" || variance === "invariant") {
      obligations.push({
        left: rightChild,
        right: leftChild,
        direction: flip(direction),
      });
    }
  }
  return obligations;
}

function flip(direction: "forward" | "backward"): "forward" | "backward" {
  return direction === "forward" ? "backward" : "forward";
}

/**
 * A new-table constructor version is only admissible when it is either a
 * previously permitted exact version or explicitly covered by a configured
 * range whose every member is backed by old-table exact versions.
 */
function checkVersionCoverage(
  leftDescriptor: ConstructorDescriptor | undefined,
  rightDescriptor: ConstructorDescriptor | undefined,
  ranges: Map<string, VersionRange>,
  obligation: InclusionObligation,
): WideningWitness | undefined {
  if (!leftDescriptor || leftDescriptor.array) {
    return undefined;
  }
  if (
    leftDescriptor.assemblyVersion === rightDescriptor?.assemblyVersion &&
    leftDescriptor.assemblyPublicKey === rightDescriptor.assemblyPublicKey
  ) {
    return undefined;
  }
  const range = ranges.get(ctorId(leftDescriptor));
  if (!range) {
    return {
      kind: "version-uncovered",
      ctorId: ctorId(leftDescriptor),
      nonterminal: obligation.left,
      detail: `new version ${leftDescriptor.assemblyVersion} has no configured version range and no exact old permit`,
    };
  }
  if (!rangeContains(range, leftDescriptor.assemblyVersion)) {
    return {
      kind: "version-uncovered",
      ctorId: ctorId(leftDescriptor),
      nonterminal: obligation.left,
      detail: "new version falls outside the configured range",
    };
  }
  return undefined;
}
