import type { DataCategory } from "./policy.ts";

export type OwnerKind = "record" | "archive";

export interface DataRecord {
  id: string;
  category: DataCategory;
  createdAtMs: number;
  archivedIn?: string;
}

export interface Replica {
  id: string;
  ownerKind: OwnerKind;
  ownerId: string;
  location: string;
}

export interface IndexEntry {
  id: string;
  ownerKind: OwnerKind;
  ownerId: string;
  indexName: string;
}

export interface Archive {
  id: string;
  category?: DataCategory;
  createdAtMs: number;
  sourceIds: string[];
  effectiveExpiryMs: number;
  proposedExpiryMs: number;
}

export interface DeletionAuditEntry {
  auditId: string;
  deletedAtMs: number;
  targetKind: OwnerKind;
  targetId: string;
  category?: DataCategory;
  expiryMs: number;
  sourceIds: string[];
  deletedReplicaIds: string[];
  deletedIndexIds: string[];
  reason: "retention_expired";
}

function ownerKey(ownerKind: OwnerKind, ownerId: string): string {
  return `${ownerKind}:${ownerId}`;
}

export class InMemoryStore {
  private readonly records = new Map<string, DataRecord>();
  private readonly archives = new Map<string, Archive>();
  private readonly replicas = new Map<string, Replica>();
  private readonly indexes = new Map<string, IndexEntry>();
  private readonly replicasByOwner = new Map<string, Set<string>>();
  private readonly indexesByOwner = new Map<string, Set<string>>();
  private readonly auditLog: DeletionAuditEntry[] = [];

  addRecord(record: DataRecord): void {
    this.records.set(record.id, { ...record });
  }

  getRecord(id: string): DataRecord | undefined {
    return this.records.get(id);
  }

  listRecords(): DataRecord[] {
    return [...this.records.values()];
  }

  deleteRecord(id: string): void {
    this.records.delete(id);
  }

  markRecordArchived(recordId: string, archiveId: string): void {
    const record = this.records.get(recordId);
    if (!record) {
      throw new Error(`Record "${recordId}" does not exist`);
    }
    record.archivedIn = archiveId;
  }

  unmarkRecordArchived(recordId: string): void {
    const record = this.records.get(recordId);
    if (record) {
      delete record.archivedIn;
    }
  }

  addArchive(archive: Archive): void {
    this.archives.set(archive.id, { ...archive, sourceIds: [...archive.sourceIds] });
  }

  getArchive(id: string): Archive | undefined {
    return this.archives.get(id);
  }

  listArchives(): Archive[] {
    return [...this.archives.values()];
  }

  deleteArchive(id: string): void {
    this.archives.delete(id);
  }

  addReplica(replica: Replica): void {
    this.replicas.set(replica.id, { ...replica });
    this.indexOwned(this.replicasByOwner, replica.ownerKind, replica.ownerId).add(replica.id);
  }

  getReplica(id: string): Replica | undefined {
    return this.replicas.get(id);
  }

  listReplicasFor(ownerKind: OwnerKind, ownerId: string): Replica[] {
    const ids = this.replicasByOwner.get(ownerKey(ownerKind, ownerId));
    return ids ? [...ids].map((id) => this.replicas.get(id)!) : [];
  }

  deleteReplica(id: string): void {
    const replica = this.replicas.get(id);
    if (!replica) {
      return;
    }
    this.replicas.delete(id);
    this.replicasByOwner
      .get(ownerKey(replica.ownerKind, replica.ownerId))
      ?.delete(id);
  }

  addIndex(index: IndexEntry): void {
    this.indexes.set(index.id, { ...index });
    this.indexOwned(this.indexesByOwner, index.ownerKind, index.ownerId).add(index.id);
  }

  getIndex(id: string): IndexEntry | undefined {
    return this.indexes.get(id);
  }

  listIndexesFor(ownerKind: OwnerKind, ownerId: string): IndexEntry[] {
    const ids = this.indexesByOwner.get(ownerKey(ownerKind, ownerId));
    return ids ? [...ids].map((id) => this.indexes.get(id)!) : [];
  }

  deleteIndex(id: string): void {
    const index = this.indexes.get(id);
    if (!index) {
      return;
    }
    this.indexes.delete(id);
    this.indexesByOwner
      .get(ownerKey(index.ownerKind, index.ownerId))
      ?.delete(id);
  }

  appendAudit(entry: DeletionAuditEntry): void {
    this.auditLog.push(entry);
  }

  getAuditLog(): readonly DeletionAuditEntry[] {
    return this.auditLog;
  }

  private indexOwned(
    index: Map<string, Set<string>>,
    ownerKind: OwnerKind,
    ownerId: string,
  ): Set<string> {
    const key = ownerKey(ownerKind, ownerId);
    let owned = index.get(key);
    if (!owned) {
      owned = new Set();
      index.set(key, owned);
    }
    return owned;
  }
}
