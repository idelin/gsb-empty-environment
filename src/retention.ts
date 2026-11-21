import { ArchiveConflictError, RetentionExtensionError } from "./errors.ts";
import type { DataCategory, RetentionPolicy } from "./policy.ts";
import { RetentionPolicyTable } from "./policy.ts";
import type { Archive, DataRecord, DeletionAuditEntry } from "./store.ts";
import { InMemoryStore } from "./store.ts";

export interface MergeInput {
  id: string;
  sourceIds: string[];
  category?: DataCategory;
  createdAtMs: number;
}

export interface SweepResult {
  deletedArchiveIds: string[];
  deletedRecordIds: string[];
  auditEntries: DeletionAuditEntry[];
}

export interface RetentionGovernanceOptions {
  policies: RetentionPolicy[];
  store?: InMemoryStore;
  nowMs?: () => number;
}

export class RetentionGovernanceService {
  private readonly policyTable: RetentionPolicyTable;
  private readonly store: InMemoryStore;
  private readonly nowMs: () => number;
  private auditSequence = 0;

  constructor(options: RetentionGovernanceOptions) {
    this.policyTable = new RetentionPolicyTable(options.policies);
    this.store = options.store ?? new InMemoryStore();
    this.nowMs = options.nowMs ?? (() => Date.now());
  }

  getStore(): InMemoryStore {
    return this.store;
  }

  expiryFor(category: DataCategory, createdAtMs: number): number {
    return this.policyTable.expiryFor(category, createdAtMs);
  }

  addRecord(record: DataRecord): void {
    if (!this.policyTable.has(record.category)) {
      this.policyTable.get(record.category);
    }
    this.store.addRecord(record);
  }

  addReplica(replica: Parameters<InMemoryStore["addReplica"]>[0]): void {
    this.store.addReplica(replica);
  }

  addIndex(index: Parameters<InMemoryStore["addIndex"]>[0]): void {
    this.store.addIndex(index);
  }

  mergeArchive(input: MergeInput): Archive {
    if (input.sourceIds.length === 0) {
      throw new ArchiveConflictError("An archive must contain at least one source record");
    }

    const sourceRecords = input.sourceIds.map((sourceId) => {
      const record = this.store.getRecord(sourceId);
      if (!record) {
        throw new ArchiveConflictError(
          `Cannot archive missing source record "${sourceId}"`,
        );
      }
      if (record.archivedIn && record.archivedIn !== input.id) {
        throw new ArchiveConflictError(
          `Record "${sourceId}" is already part of archive "${record.archivedIn}"`,
        );
      }
      return record;
    });

    const sourceExpiries = sourceRecords.map((record) =>
      this.policyTable.expiryFor(record.category, record.createdAtMs),
    );
    const earliestSourceExpiry = Math.min(...sourceExpiries);

    let proposedExpiry = earliestSourceExpiry;
    if (input.category) {
      proposedExpiry = this.policyTable.expiryFor(
        input.category,
        input.createdAtMs,
      );
    }

    if (proposedExpiry > earliestSourceExpiry) {
      throw new RetentionExtensionError(
        `Refusing to merge into archive "${input.id}": proposed expiry ${proposedExpiry} ` +
          `would extend retention beyond the earliest source expiry ${earliestSourceExpiry}`,
      );
    }

    const archive: Archive = {
      id: input.id,
      category: input.category,
      createdAtMs: input.createdAtMs,
      sourceIds: [...input.sourceIds],
      effectiveExpiryMs: earliestSourceExpiry,
      proposedExpiryMs: proposedExpiry,
    };
    this.store.addArchive(archive);

    for (const sourceId of input.sourceIds) {
      this.store.markRecordArchived(sourceId, input.id);
    }

    return archive;
  }

  runRetentionSweep(nowMs: number = this.nowMs()): SweepResult {
    const deletedArchiveIds: string[] = [];
    const deletedRecordIds: string[] = [];
    const auditEntries: DeletionAuditEntry[] = [];

    for (const archive of this.store.listArchives()) {
      if (archive.effectiveExpiryMs <= nowMs) {
        auditEntries.push(this.deleteArchive(archive, nowMs));
        deletedArchiveIds.push(archive.id);
      }
    }

    for (const record of this.store.listRecords()) {
      const expiryMs = this.policyTable.expiryFor(
        record.category,
        record.createdAtMs,
      );
      if (expiryMs <= nowMs) {
        auditEntries.push(this.deleteRecord(record, expiryMs, nowMs));
        deletedRecordIds.push(record.id);
      }
    }

    return { deletedArchiveIds, deletedRecordIds, auditEntries };
  }

  private deleteRecord(record: DataRecord, expiryMs: number, nowMs: number): DeletionAuditEntry {
    const deletedReplicaIds = this.deleteAllReplicas("record", record.id);
    const deletedIndexIds = this.deleteAllIndexes("record", record.id);
    this.store.deleteRecord(record.id);

    const entry: DeletionAuditEntry = {
      auditId: this.nextAuditId(nowMs),
      deletedAtMs: nowMs,
      targetKind: "record",
      targetId: record.id,
      category: record.category,
      expiryMs,
      sourceIds: [],
      deletedReplicaIds,
      deletedIndexIds,
      reason: "retention_expired",
    };
    this.store.appendAudit(entry);
    return entry;
  }

  private deleteArchive(archive: Archive, nowMs: number): DeletionAuditEntry {
    const deletedReplicaIds = this.deleteAllReplicas("archive", archive.id);
    const deletedIndexIds = this.deleteAllIndexes("archive", archive.id);

    for (const sourceId of archive.sourceIds) {
      this.store.unmarkRecordArchived(sourceId);
    }
    this.store.deleteArchive(archive.id);

    const entry: DeletionAuditEntry = {
      auditId: this.nextAuditId(nowMs),
      deletedAtMs: nowMs,
      targetKind: "archive",
      targetId: archive.id,
      category: archive.category,
      expiryMs: archive.effectiveExpiryMs,
      sourceIds: [...archive.sourceIds],
      deletedReplicaIds,
      deletedIndexIds,
      reason: "retention_expired",
    };
    this.store.appendAudit(entry);
    return entry;
  }

  private deleteAllReplicas(
    ownerKind: "record" | "archive",
    ownerId: string,
  ): string[] {
    const replicaIds = this.store
      .listReplicasFor(ownerKind, ownerId)
      .map((replica) => replica.id);
    for (const replicaId of replicaIds) {
      this.store.deleteReplica(replicaId);
    }
    return replicaIds;
  }

  private deleteAllIndexes(
    ownerKind: "record" | "archive",
    ownerId: string,
  ): string[] {
    const indexIds = this.store
      .listIndexesFor(ownerKind, ownerId)
      .map((index) => index.id);
    for (const indexId of indexIds) {
      this.store.deleteIndex(indexId);
    }
    return indexIds;
  }

  private nextAuditId(nowMs: number): string {
    this.auditSequence += 1;
    return `audit-${nowMs}-${this.auditSequence}`;
  }
}
