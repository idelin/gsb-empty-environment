import { canonicalJson, ctorId, ctorKey, sha256Hex } from "./canonical.ts";
import {
  assertRangesBackedByOldTable,
  mapNewTable,
  rangeContains,
  validateDeployment,
} from "./deployment.ts";
import { canonicalizeGrammar } from "./grammar.ts";
import { decideInclusion } from "./inclusion.ts";
import type {
  CanonicalGrammar,
  CanonicalProduction,
} from "./types.ts";
import type {
  BindingTable,
  CanonicalGrammar,
  DeploymentConfig,
  GrammarDocument,
  InclusionCertificate,
  InclusionObligation,
  VersionRange,
  WideningWitness,
} from "./types.ts";

export interface IssueInput {
  oldTable: BindingTable;
  newTable: BindingTable;
  oldGrammar: GrammarDocument;
  newGrammar: GrammarDocument;
  config: DeploymentConfig;
  now?: Date;
}

export interface IssueResult {
  certificate: InclusionCertificate;
  publishable: boolean;
  leftGrammar: CanonicalGrammar;
  rightGrammar: CanonicalGrammar;
  ranges: Map<string, VersionRange>;
}

/**
 * Rewrite the canonical new grammar into old-table coordinates. Productions
 * whose constructor has no old-side identity are dropped; they are exactly
 * the terms that would widen old permits and are caught by the inclusion run.
 */
function rewriteLeftGrammar(
  left: CanonicalGrammar,
  mapped: ReturnType<typeof mapNewTable>,
  newTable: BindingTable,
  config: DeploymentConfig,
  right: CanonicalGrammar,
): WideningWitness[] {
  const newIdToMappedId = new Map<string, string>();
  const mappedCatalog = new Map(left.catalog);
  const dropped: WideningWitness[] = [];
  for (const entry of mapped) {
    const rename = config.renames.find(
      (rule) =>
        ctorKey(rule.to.assemblyPublicKey, rule.to.typeName) ===
        ctorKey(entry.descriptor.assemblyPublicKey, entry.descriptor.typeName),
    );
    const sourceName = rename ? rename.from : {
      assemblyPublicKey: entry.descriptor.assemblyPublicKey,
      typeName: entry.descriptor.typeName,
    };
    const source = newTable.constructors.find(
      (descriptor) =>
        ctorKey(descriptor.assemblyPublicKey, descriptor.typeName) ===
        ctorKey(sourceName.assemblyPublicKey, sourceName.typeName),
    );
    if (source) {
      newIdToMappedId.set(ctorId(source), ctorId(entry.descriptor));
      mappedCatalog.set(ctorId(entry.descriptor), entry.descriptor);
    }
  }

  for (const [nonterminal, productions] of left.nonterminals) {
    const retained: CanonicalProduction[] = [];
    for (const production of productions) {
      const target = newIdToMappedId.get(production.ctorId);
      if (target && right.catalog.has(target)) {
        retained.push({ ctorId: target, children: [...production.children] });
      } else {
        dropped.push({
          kind: "unmatched-production",
          ctorId: production.ctorId,
          nonterminal,
          detail: `new-table constructor ${production.ctorId} has no certified old-table identity`,
        });
      }
    }
    left.nonterminals.set(nonterminal, retained);
  }
  left.catalog.clear();
  for (const [id, descriptor] of mappedCatalog) {
    if (right.catalog.has(id)) {
      left.catalog.set(id, descriptor);
    }
  }
  left.digest = sha256Hex({
    start: left.start,
    nonterminals: [...left.nonterminals.entries()].map(([id, productions]) => ({
      id,
      productions,
    })),
  });
  return dropped;
}

/**
 * Certify a new binding table against an old one. A release is publishable
 * only when the verdict is `subset` and the budget was not exhausted;
 * `widened` and `insufficient` certificates are emitted for audit but cannot
 * be used by the runtime admission path.
 */
export function issueCertificate(input: IssueInput): IssueResult {
  const { config, oldTable, newTable } = input;
  validateDeployment(config, oldTable, newTable);
  if (input.oldGrammar.tenant !== input.newGrammar.tenant) {
    throw new Error("grammar documents must describe the same tenant");
  }

  const mapped = mapNewTable(config, oldTable, newTable);
  assertRangesBackedByOldTable(mapped, oldTable);

  const ranges = new Map<string, VersionRange>();
  for (const entry of mapped) {
    if (entry.range) {
      ranges.set(ctorId(entry.descriptor), entry.range);
    }
  }

  const left = canonicalizeGrammar(input.newGrammar, newTable);
  const right = canonicalizeGrammar(input.oldGrammar, oldTable);
  const droppedWitnesses = rewriteLeftGrammar(left, mapped, newTable, config, right);
  const run = decideInclusion({
    tenant: input.oldGrammar.tenant,
    leftTableId: newTable.tableId,
    rightTableId: oldTable.tableId,
    left,
    right,
    ranges,
    budget: config.budget,
    issuedAt: (input.now ?? new Date(0)).toISOString(),
    rulesVersion: config.rulesVersion,
  });
  run.certificate.witnesses.unshift(...droppedWitnesses);
  if (droppedWitnesses.length > 0 && run.certificate.verdict === "subset") {
    run.certificate.verdict = "widened";
  }
  const certificate = sealCertificate(run.certificate);
  return {
    certificate,
    publishable: certificate.verdict === "subset" && !run.exhaustedBudget,
    leftGrammar: left,
    rightGrammar: right,
    ranges,
  };
}

export function sealCertificate(
  certificate: InclusionCertificate,
): InclusionCertificate {
  const { digest: _digest, ...body } = certificate;
  const sealed: InclusionCertificate = {
    ...certificate,
    digest: sha256Hex(body),
  };
  return sealed;
}

export interface VerificationReport {
  valid: boolean;
  errors: string[];
}

/**
 * Independently check a certificate. The verifier only needs the two
 * canonical grammars and the certificate itself; it replays every obligation
 * in the supplied proof and rejects missing or non-productive obligations.
 */
export function verifyCertificate(
  certificate: InclusionCertificate,
  left: CanonicalGrammar,
  right: CanonicalGrammar,
  ranges: Map<string, VersionRange>,
): VerificationReport {
  const errors: string[] = [];
  const { digest, ...body } = certificate;
  if (sha256Hex(body) !== digest) {
    errors.push("certificate digest mismatch");
  }
  if (certificate.leftGrammarDigest !== left.digest) {
    errors.push("left grammar digest mismatch");
  }
  if (certificate.rightGrammarDigest !== right.digest) {
    errors.push("right grammar digest mismatch");
  }
  if (certificate.spent > certificate.budget) {
    errors.push("certificate exceeded its own budget");
  }
  if (
    certificate.startPair.left !== left.start ||
    certificate.startPair.right !== right.start
  ) {
    errors.push("start pair does not match grammar roots");
  }

  const proofKeys = new Set<string>();
  for (const obligation of certificate.proof) {
    proofKeys.add(obligationKey(obligation));
  }
  if (!proofKeys.has(obligationKey(certificate.startPair))) {
    errors.push("start pair is missing from the proof");
  }
  for (const obligation of certificate.proof) {
    for (const error of checkObligation(obligation, left, right, ranges, proofKeys)) {
      errors.push(error);
    }
  }
  if (certificate.verdict === "subset" && certificate.witnesses.length !== 0) {
    errors.push("subset certificate must not carry widening witnesses");
  }
  if (
    (certificate.verdict === "widened" ||
      certificate.verdict === "insufficient") &&
    certificate.witnesses.length === 0
  ) {
    errors.push(`${certificate.verdict} certificate must explain the verdict`);
  }
  return { valid: errors.length === 0, errors };
}

function obligationKey(obligation: InclusionObligation): string {
  return `${obligation.direction}:${obligation.left}<=${obligation.right}`;
}

function checkObligation(
  obligation: InclusionObligation,
  left: CanonicalGrammar,
  right: CanonicalGrammar,
  ranges: Map<string, VersionRange>,
  proofKeys: Set<string>,
): string[] {
  const errors: string[] = [];
  const sourceGrammar = obligation.direction === "forward" ? left : right;
  const targetGrammar = obligation.direction === "forward" ? right : left;
  const source =
    sourceGrammar.nonterminals.get(obligation.left) ?? [];
  const target =
    targetGrammar.nonterminals.get(obligation.right) ?? [];
  if (source.length === 0) {
    return errors;
  }
  if (target.length === 0) {
    errors.push(`obligation ${obligationKey(obligation)} targets an empty language`);
    return errors;
  }
  for (const production of source) {
    const match = target.find((candidate) => candidate.ctorId === production.ctorId);
    if (!match) {
      errors.push(
        `obligation ${obligationKey(obligation)} leaves production ${production.ctorId} unmatched`,
      );
      continue;
    }
    const descriptor = targetGrammar.catalog.get(match.ctorId);
    if (!descriptor) {
      errors.push(`missing catalog entry for ${match.ctorId}`);
      continue;
    }
    for (let slot = 0; slot < descriptor.variances.length; slot++) {
      const variance = descriptor.variances[slot];
      const required: InclusionObligation[] = [];
      if (variance === "out" || variance === "invariant") {
        required.push({
          left: production.children[slot],
          right: match.children[slot],
          direction: obligation.direction,
        });
      }
      if (variance === "in" || variance === "invariant") {
        required.push({
          left: match.children[slot],
          right: production.children[slot],
          direction:
            obligation.direction === "forward" ? "backward" : "forward",
        });
      }
      for (const child of required) {
        if (!proofKeys.has(obligationKey(child))) {
          errors.push(
            `proof misses required obligation ${obligationKey(child)}`,
          );
        }
      }
    }
    if (!production.ctorId.startsWith("array:") && production.ctorId !== match.ctorId) {
      const range = ranges.get(production.ctorId);
      const sourceDescriptor = sourceGrammar.catalog.get(production.ctorId);
      if (
        !range ||
        !sourceDescriptor ||
        !rangeContains(range, sourceDescriptor.assemblyVersion)
      ) {
        errors.push(`version ${production.ctorId} lacks a covering range`);
      }
    }
  }
  return errors;
}

export function certificateCanonicalJson(certificate: InclusionCertificate): string {
  return canonicalJson(certificate);
}
