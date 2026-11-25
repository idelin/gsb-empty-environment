import { deepFreeze, fingerprint } from './fingerprint.ts';
import type {
  EvidenceEvent,
  EvidenceSnapshot,
  MissingEventPage,
  UnixMillis,
} from './types.ts';

export interface CreateSnapshotOptions {
  readonly knownMissingPages?: readonly MissingEventPage[];
  readonly createdAt?: UnixMillis;
  readonly startSequence?: number;
}

export function createEvidenceSnapshot(
  events: readonly EvidenceEvent[],
  options: CreateSnapshotOptions = {},
): EvidenceSnapshot {
  const startSequence = options.startSequence ?? 0;
  const clonedEvents = events.map((event, index) => {
    const clone = JSON.parse(JSON.stringify(event)) as EvidenceEvent;
    if (clone.ingestSequence === undefined) {
      (clone as { ingestSequence?: number }).ingestSequence = startSequence + index;
    }
    return clone;
  });

  const knownMissingPages = (options.knownMissingPages ?? []).map((page) =>
    JSON.parse(JSON.stringify(page)) as MissingEventPage,
  );

  const snapshotWithoutHash = {
    schemaVersion: 1 as const,
    createdAt: options.createdAt ?? 0,
    events: clonedEvents,
    knownMissingPages,
  };
  const snapshotHash = fingerprint(snapshotWithoutHash);
  const byId: Record<string, EvidenceEvent> = {};
  for (const event of clonedEvents) {
    if (byId[event.id]) {
      throw new Error(`Duplicate evidence event id: ${event.id}`);
    }
    byId[event.id] = event;
  }

  const snapshot: EvidenceSnapshot = {
    schemaVersion: 1,
    frozen: true,
    snapshotHash,
    createdAt: snapshotWithoutHash.createdAt,
    events: clonedEvents,
    byId,
    order: clonedEvents.map((event) => event.id),
    knownMissingPages,
  };
  return deepFreeze(snapshot);
}

export function verifySnapshotIntegrity(snapshot: EvidenceSnapshot): boolean {
  if (!Object.isFrozen(snapshot) || snapshot.frozen !== true) {
    return false;
  }
  const expectedHash = fingerprint({
    schemaVersion: snapshot.schemaVersion,
    createdAt: snapshot.createdAt,
    events: snapshot.events,
    knownMissingPages: snapshot.knownMissingPages,
  });
  if (expectedHash !== snapshot.snapshotHash) {
    return false;
  }
  return snapshot.events.every((event) => Object.isFrozen(event));
}

export function isScopeMatch(
  candidate: {
    readonly tenantId?: string;
    readonly principalId?: string;
    readonly topLevelSite?: string;
    readonly endpointGroupId?: string;
    readonly documentGeneration?: string;
    readonly bucketKey?: string;
  },
  scope: MissingEventPage['scope'],
): boolean {
  if (!scope) {
    return true;
  }
  return (
    (scope.tenantId === undefined || scope.tenantId === candidate.tenantId) &&
    (scope.principalId === undefined || scope.principalId === candidate.principalId) &&
    (scope.topLevelSite === undefined || scope.topLevelSite === candidate.topLevelSite) &&
    (scope.endpointGroupId === undefined ||
      scope.endpointGroupId === candidate.endpointGroupId) &&
    (scope.documentGeneration === undefined ||
      scope.documentGeneration === candidate.documentGeneration) &&
    (scope.bucketKey === undefined || scope.bucketKey === candidate.bucketKey)
  );
}
