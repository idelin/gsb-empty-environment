import type { SubOperationSpec } from "./types.ts";

/**
 * Centralized key layout. Sub-operation keys always include grant AND parent
 * action, so two parent actions reusing the same subId never collide.
 */

export function snapshotKey(grantId: string, actionId: string, idemKey: string): string {
  return `proof/snapshot/${grantId}/${actionId}/${idemKey}`;
}

export function claimKey(grantId: string, actionId: string): string {
  return `ledger/active-claim/${grantId}/${actionId}`;
}

/** Immutable claim ticket; retained forever for audit, never overwritten. */
export function claimTicketKey(
  grantId: string,
  actionId: string,
  idemKey: string,
): string {
  return `ledger/claim/${grantId}/${actionId}/${idemKey}`;
}

export function decisionKey(grantId: string, actionId: string, idemKey: string): string {
  return `ledger/decision/${grantId}/${actionId}/${idemKey}`;
}

export function subKey(
  grantId: string,
  actionId: string,
  idemKey: string,
  subId: string,
): string {
  return `proof/sub/${grantId}/${actionId}/${idemKey}/${subId}`;
}

export function subCounterKey(grantId: string, actionId: string, subId: string): string {
  return `counter/sub/${grantId}/${actionId}/${subId}`;
}

export function decisionPrefix(grantId: string, actionId: string): string {
  return `ledger/decision/${grantId}/${actionId}/`;
}

export function subProofPrefix(grantId: string, actionId: string): string {
  return `proof/sub/${grantId}/${actionId}/`;
}

export function snapshotPrefix(grantId: string, actionId: string): string {
  return `proof/snapshot/${grantId}/${actionId}/`;
}

/** Deterministic Kahn topological sort; rejects cycles and unknown deps. */
export function topoSort(specs: readonly SubOperationSpec[]): SubOperationSpec[] {
  const byId = new Map(specs.map((spec) => [spec.subId, spec]));
  const indegree = new Map<string, number>();
  const dependents = new Map<string, string[]>();
  for (const spec of specs) {
    indegree.set(spec.subId, 0);
    dependents.set(spec.subId, []);
  }
  for (const spec of specs) {
    for (const dependency of spec.dependsOn) {
      if (!byId.has(dependency)) {
        throw new Error(`unknown dependency ${dependency} for sub ${spec.subId}`);
      }
      indegree.set(spec.subId, (indegree.get(spec.subId) ?? 0) + 1);
      dependents.get(dependency)!.push(spec.subId);
    }
  }
  const ready = [...indegree.entries()]
    .filter(([, degree]) => degree === 0)
    .map(([id]) => id)
    .sort();
  const ordered: SubOperationSpec[] = [];
  while (ready.length > 0) {
    const id = ready.shift()!;
    ordered.push(byId.get(id)!);
    for (const dependent of dependents.get(id)!) {
      const nextDegree = indegree.get(dependent)! - 1;
      indegree.set(dependent, nextDegree);
      if (nextDegree === 0) {
        ready.push(dependent);
        ready.sort();
      }
    }
  }
  if (ordered.length !== specs.length) {
    throw new Error("dependency cycle detected in sub-operations");
  }
  return ordered;
}
