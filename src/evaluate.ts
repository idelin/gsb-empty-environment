import { anyRegionCovers, knownPurpose, knownRegion } from "./catalog.ts";
import type { AliasDirectoryView } from "./alias.ts";
import type {
  BatchDecision,
  BatchEvaluation,
  BatchItem,
  CoveringChain,
  CoverageQuery,
  DeniedReason,
  Evidence,
  PendingReason,
  PurposeCatalog,
  RegionCatalog,
  SenderId,
  SenderReceipt,
  SubjectId,
  Timestamp,
} from "./types.ts";
import type { MaterializedChain } from "./materialize.ts";

export interface ChainIndex {
  chains(): readonly MaterializedChain[];
}

function evidenceDedup(evidence: Evidence[]): Evidence[] {
  const byId = new Map<string, Evidence>();
  for (const item of evidence) {
    byId.set(item.statementId, item);
  }
  return [...byId.values()].sort((a, b) => a.seq - b.seq ||
    a.statementId.localeCompare(b.statementId));
}

/**
 * Return every chain that covers the whole query on its own. Consent from two
 * different chains is never merged: a send must be justified by one chain that
 * supplies subject, purpose (subtree), sender, region and time together.
 */
export function coveringChainsFor(
  index: ChainIndex,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
  query: CoverageQuery,
): CoveringChain[] {
  const result: CoveringChain[] = [];
  for (const chain of index.chains()) {
    if (chain.isUnusable() || !chain.subjects.has(query.subject)) {
      continue;
    }
    const grantEvidence: Evidence[] = [];
    const revocationEvidence: Evidence[] = [];
    let covered = false;
    for (const cell of chain.cells) {
      if (cell.subject !== query.subject) {
        continue;
      }
      if (cell.sender !== query.sender) {
        continue;
      }
      if (!anyRegionCovers(regions, [cell.region], query.region)) {
        continue;
      }
      if (!cell.covers(query.purpose, query.at)) {
        continue;
      }
      covered = true;
      grantEvidence.push(cell.grantEvidence);
      for (const id of cell.appliedRevocationIds(query.purpose, query.at)) {
        const evidence = chain.evidenceById.get(id);
        if (evidence) {
          revocationEvidence.push(evidence);
        }
      }
    }
    if (covered) {
      result.push({
        chainId: chain.chainId,
        grants: evidenceDedup(grantEvidence),
        revocations: evidenceDedup(revocationEvidence),
      });
    }
  }
  return result;
}

function decide(
  index: ChainIndex,
  aliases: AliasDirectoryView,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
  item: BatchItem,
  at: Timestamp,
): BatchDecision {
  const pending = (reason: PendingReason): BatchDecision => ({
    itemId: item.itemId,
    status: "pending",
    reason,
  });
  const denied = (reason: DeniedReason): BatchDecision => ({
    itemId: item.itemId,
    status: "denied",
    reason,
  });

  if (!knownPurpose(purposes, item.purpose)) {
    return pending("unknown-purpose");
  }
  if (!knownRegion(regions, item.region)) {
    return pending("unknown-region");
  }

  const resolution = aliases.resolve(item.recipient);
  if (resolution.status === "unknown") {
    return pending("unknown-recipient");
  }
  if (resolution.status === "ambiguous") {
    return pending("ambiguous-recipient");
  }
  const subject = resolution.subject as SubjectId;

  // A chain with any integrity problem (bad signature, malformed statement,
  // issuer fork) can never authorize. If it concerns this subject, the item is
  // ambiguous and must stay pending rather than being denied.
  if (hasAnyUnusableChainFor(index, subject)) {
    const unusable = index
      .chains()
      .find((chain) => chain.isUnusable() && chain.subjects.has(subject));
    const reason: PendingReason = unusable?.issues.has("forked-chain")
      ? "forked-chain"
      : "unverified-signature";
    return pending(reason);
  }

  const candidates = coveringChainsFor(index, purposes, regions, {
    subject,
    purpose: item.purpose,
    sender: item.sender,
    region: item.region,
    at,
  });

  if (item.chainHint) {
    const pinned = candidates.filter(
      (candidate) => candidate.chainId === item.chainHint,
    );
    if (pinned.length === 0) {
      // Other chains may cover it, but the pin forbids relying on them.
      return pending("pinned-chain-mismatch");
    }
    return {
      itemId: item.itemId,
      status: "deliverable",
      basis: {
        chainId: pinned[0].chainId,
        grants: pinned[0].grants,
        revocations: pinned[0].revocations,
      },
    };
  }

  if (candidates.length === 0) {
    return denied("no-consent");
  }

  return {
    itemId: item.itemId,
    status: "deliverable",
    basis: {
      chainId: candidates[0].chainId,
      grants: candidates[0].grants,
      revocations: candidates[0].revocations,
    },
  };
}

function hasAnyUnusableChainFor(
  index: ChainIndex,
  subject: SubjectId,
): boolean {
  return index
    .chains()
    .some((chain) => chain.isUnusable() && chain.subjects.has(subject));
}

export function evaluateBatch(
  index: ChainIndex,
  aliases: AliasDirectoryView,
  purposes: PurposeCatalog,
  regions: RegionCatalog,
  items: readonly BatchItem[],
  at: Timestamp,
): BatchEvaluation {
  const all = items.map((item) =>
    decide(index, aliases, purposes, regions, item, at),
  );
  return {
    deliverable: all.filter((decision) => decision.status === "deliverable"),
    pending: all.filter((decision) => decision.status === "pending"),
    denied: all.filter((decision) => decision.status === "denied"),
    all,
  };
}

/**
 * Build per-sender receipts. A sender only sees decisions for items it itself
 * handles. The response contains no information about whether any other sender
 * holds consent, so cross-sender inference is impossible.
 */
export function buildSenderReceipts(
  items: readonly BatchItem[],
  evaluation: BatchEvaluation,
): SenderReceipt[] {
  const bySender = new Map<SenderId, BatchDecision[]>();
  const decisionByItem = new Map(
    evaluation.all.map((decision) => [decision.itemId, decision]),
  );
  for (const item of items) {
    const decision = decisionByItem.get(item.itemId);
    if (!decision) {
      continue;
    }
    const list = bySender.get(item.sender) ?? [];
    list.push(decision);
    bySender.set(item.sender, list);
  }
  return [...bySender.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([sender, results]) => ({ sender, results }));
}
