import { knownPurpose, knownRegion, purposeCovers } from "./catalog.ts";
import {
  containsAt,
  intersect,
  isEmpty,
  subtractAll,
  type HalfOpenInterval,
} from "./intervals.ts";
import { canonicalDigest } from "./signing.ts";
import type {
  ChainId,
  ConsentStatement,
  Evidence,
  GrantStatement,
  PurposeCatalog,
  PurposeNodeId,
  RegionCatalog,
  RegionCode,
  RevocationStatement,
  SenderId,
  SignedConsentStatement,
  StatementId,
  SubjectId,
  Timestamp,
} from "./types.ts";

export type ChainIssue =
  | "unverified-signature"
  | "malformed-statement"
  | "forked-chain";

interface RevocationCut {
  readonly revocationId: StatementId;
  readonly purposeNodes: readonly PurposeNodeId[] | undefined;
  readonly interval: HalfOpenInterval | undefined;
}

export interface EffectiveCell {
  readonly grantId: StatementId;
  readonly grantEvidence: Evidence;
  readonly subject: SubjectId;
  readonly grantPurpose: PurposeNodeId;
  readonly sender: SenderId;
  readonly region: RegionCode;
  readonly base: HalfOpenInterval;
  readonly cuts: RevocationCut[];
  appliedRevocationIds(requestedPurpose: PurposeNodeId, at: Timestamp): StatementId[];
  covers(requestedPurpose: PurposeNodeId, at: Timestamp): boolean;
}

export interface MaterializedChain {
  readonly chainId: ChainId;
  readonly issuer: string;
  readonly issues: ReadonlySet<ChainIssue>;
  readonly cells: readonly EffectiveCell[];
  readonly statementIds: ReadonlySet<StatementId>;
  readonly subjects: ReadonlySet<SubjectId>;
  readonly evidenceById: ReadonlyMap<StatementId, Evidence>;
  isUnusable(): boolean;
}

interface ResolvedInput {
  statement: ConsentStatement;
  signature: string;
  verified: boolean;
}

function toEvidence(input: ResolvedInput): Evidence {
  return {
    statementId: input.statement.id,
    chainId: input.statement.chainId,
    kind: input.statement.kind,
    seq: input.statement.seq,
    signature: input.signature,
  };
}

/**
 * Apply explicit corrections. The newest correction (by issuedAt) replaces the
 * targeted statement wholesale. Corrections are keyed by statement id, so the
 * result is independent of transport/arrival order.
 */
function resolveCorrections(inputs: readonly ResolvedInput[]): {
  logical: ResolvedInput[];
  malformed: number;
  conflictingCorrections: boolean;
} {
  const originals = new Map<StatementId, ResolvedInput>();
  const corrections = new Map<
    StatementId,
    { winner: ResolvedInput; digest: string }
  >();
  let malformed = 0;
  let conflictingCorrections = false;

  for (const input of inputs) {
    const statement = input.statement;
    if (statement.kind !== "correction") {
      const existing = originals.get(statement.id);
      if (!existing) {
        originals.set(statement.id, input);
      } else {
        const digest = canonicalDigest(statement).toString("base64");
        const priorDigest = canonicalDigest(existing.statement).toString("base64");
        if (digest !== priorDigest) {
          conflictingCorrections = true;
        }
      }
      continue;
    }
    const digest = canonicalDigest(statement.replacement).toString("base64");
    const existing = corrections.get(statement.correctsId);
    if (!existing) {
      corrections.set(statement.correctsId, {
        winner: {
          statement: statement.replacement,
          signature: input.signature,
          verified: input.verified,
        },
        digest,
      });
    } else {
      if (existing.digest !== digest) {
        conflictingCorrections = true;
        if (statement.issuedAt >= existing.winner.statement.issuedAt) {
          corrections.set(statement.correctsId, {
            winner: {
              statement: statement.replacement,
              signature: input.signature,
              verified: input.verified,
            },
            digest,
          });
        }
      }
    }
  }

  const logical: ResolvedInput[] = [];
  for (const [id, input] of originals) {
    const correction = corrections.get(id);
    logical.push(correction ? correction.winner : input);
  }
  for (const [id, correction] of corrections) {
    if (!originals.has(id)) {
      logical.push(correction.winner);
    }
  }
  return { logical, malformed, conflictingCorrections };
}

/** Same (chainId, seq) carrying different payloads is an issuer fork. */
function detectFork(logical: readonly ResolvedInput[]): boolean {
  const seen = new Map<string, Set<string>>();
  for (const input of logical) {
    const statement = input.statement;
    const key = `${statement.chainId}#${statement.seq}`;
    const digest = canonicalDigest(statement).toString("base64");
    const digests = seen.get(key);
    if (digests && !digests.has(digest)) {
      return true;
    }
    if (!digests) {
      seen.set(key, new Set([digest]));
    } else {
      digests.add(digest);
    }
  }
  return false;
}

function makeCells(
  grant: GrantStatement,
  evidence: Evidence,
  purposes: PurposeCatalog,
): EffectiveCell[] {
  const base: HalfOpenInterval = {
    from: grant.validFrom,
    to: grant.validUntil,
  };
  const cells: EffectiveCell[] = [];
  for (const sender of grant.senders) {
    for (const region of grant.regions) {
      const cuts: RevocationCut[] = [];
      cells.push({
        grantId: grant.id,
        grantEvidence: evidence,
        subject: grant.subject,
        grantPurpose: grant.purpose,
        sender,
        region,
        base,
        cuts,
        appliedRevocationIds(requestedPurpose, at) {
          const applied: StatementId[] = [];
          for (const cut of cuts) {
            if (!cutMatchesPurpose(cut, requestedPurpose)) {
              continue;
            }
            const window = intersect(base, cut.interval ?? base);
            if (containsAt(window, at)) {
              applied.push(cut.revocationId);
            }
          }
          return applied;
        },
        covers(requestedPurpose, at) {
          if (!purposeCovers(purposes, grant.purpose, requestedPurpose)) {
            return false;
          }
          const windows = cuts
            .filter((cut) => cutMatchesPurpose(cut, requestedPurpose))
            .map((cut) => intersect(base, cut.interval ?? base));
          const surviving = subtractAll([base], windows);
          return surviving.some((piece) => containsAt(piece, at));
        },
      });
    }
  }
  return cells;
}

function cutMatchesPurpose(
  cut: RevocationCut,
  requestedPurpose: PurposeNodeId,
): boolean {
  if (!cut.purposeNodes) {
    return true;
  }
  return cut.purposeNodes.some(
    (node) => node === requestedPurpose || requestedPurpose.startsWith(`${node}.`),
  );
}

function validGrant(
  grant: GrantStatement,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
): boolean {
  if (grant.validUntil <= grant.validFrom) {
    return false;
  }
  if (!knownPurpose(purposes, grant.purpose)) {
    return false;
  }
  if (grant.senders.length === 0 || grant.regions.length === 0) {
    return false;
  }
  return grant.regions.every((region) => knownRegion(regions, region));
}

/**
 * A recovery must not overlap the lapsed statement it follows. Half-open
 * intervals may touch at the boundary; actual overlap is rejected.
 */
function validRecovery(
  recovery: Extract<ConsentStatement, { kind: "recovery" }>,
  grantsById: ReadonlyMap<StatementId, GrantStatement>,
): boolean {
  const prior = grantsById.get(recovery.supersedes);
  return prior !== undefined && prior.validUntil <= recovery.validFrom;
}

function revocationMatchesCell(
  revocation: RevocationStatement,
  cell: EffectiveCell,
): boolean {
  if (revocation.subject !== cell.subject) {
    return false;
  }
  if (
    revocation.targetGrantIds &&
    !revocation.targetGrantIds.includes(cell.grantId)
  ) {
    return false;
  }
  const filter = revocation.filter;
  if (filter.senders && !filter.senders.includes(cell.sender)) {
    return false;
  }
  if (filter.regions && !filter.regions.includes(cell.region)) {
    return false;
  }
  if (
    filter.purposes &&
    !filter.purposes.some(
      (node) =>
        node === cell.grantPurpose || node.startsWith(`${cell.grantPurpose}.`),
    )
  ) {
    return false;
  }
  if (filter.interval && isEmpty(intersect(filter.interval, cell.base))) {
    return false;
  }
  return true;
}

export function materializeChain(
  chainId: ChainId,
  envelopes: readonly SignedConsentStatement[],
  verify: (envelope: SignedConsentStatement) => boolean,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
): MaterializedChain {
  const inputs: ResolvedInput[] = envelopes.map((envelope) => ({
    statement: envelope.statement,
    signature: envelope.signature,
    verified: verify(envelope),
  }));

  const issues = new Set<ChainIssue>();
  if (inputs.some((input) => !input.verified)) {
    issues.add("unverified-signature");
  }

  const { logical, malformed, conflictingCorrections } = resolveCorrections(inputs);
  if (malformed > 0) {
    issues.add("malformed-statement");
  }
  if (conflictingCorrections || detectFork(logical)) {
    issues.add("forked-chain");
  }

  const evidenceById = new Map<StatementId, Evidence>();
  const statementIds = new Set<StatementId>();
  const subjects = new Set<SubjectId>();
  const grants: GrantStatement[] = [];
  const grantsById = new Map<StatementId, GrantStatement>();
  const grantEvidence = new Map<StatementId, Evidence>();
  const revocations: RevocationStatement[] = [];
  const revocationEvidence = new Map<StatementId, Evidence>();

  for (const input of logical) {
    const statement = input.statement;
    statementIds.add(statement.id);
    evidenceById.set(statement.id, toEvidence(input));
    if (statement.kind === "grant" || statement.kind === "recovery") {
      subjects.add(statement.subject);
      if (!validGrant(statement, purposes, regions)) {
        issues.add("malformed-statement");
        continue;
      }
      if (statement.kind === "recovery" && !validRecovery(statement, grantsById)) {
        issues.add("malformed-statement");
        continue;
      }
      grants.push(statement);
      grantsById.set(statement.id, statement);
      grantEvidence.set(statement.id, toEvidence(input));
    } else if (statement.kind === "revocation") {
      subjects.add(statement.subject);
      revocations.push(statement);
      revocationEvidence.set(statement.id, toEvidence(input));
    }
  }

  const cells = grants.flatMap((grant) =>
    makeCells(grant, grantEvidence.get(grant.id) as Evidence, purposes),
  );

  for (const revocation of [...revocations].sort(
    (a, b) => a.issuedAt - b.issuedAt || a.seq - b.seq,
  )) {
    for (const cell of cells) {
      if (revocationMatchesCell(revocation, cell)) {
        cell.cuts.push({
          revocationId: revocation.id,
          purposeNodes: revocation.filter.purposes,
          interval: revocation.filter.interval,
        });
      }
    }
  }

  return {
    chainId,
    issuer: logical[0]?.statement.issuer ?? "",
    issues,
    cells,
    statementIds,
    subjects,
    evidenceById,
    isUnusable() {
      return issues.size > 0;
    },
  };
}
