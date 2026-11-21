import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { RetentionExtensionError, RetentionPolicyError } from "../src/errors.ts";
import type { DataCategory, RetentionPolicy } from "../src/policy.ts";
import { InMemoryStore } from "../src/store.ts";
import { RetentionGovernanceService } from "../src/retention.ts";

const DAY = 24 * 60 * 60 * 1000;
const NOW = 1_000_000 * DAY;

const POLICIES: RetentionPolicy[] = [
  { category: "metrics", retentionMs: 7 * DAY },
  { category: "logs", retentionMs: 30 * DAY },
  { category: "events", retentionMs: 1 * DAY },
];

function makeService(): RetentionGovernanceService {
  return new RetentionGovernanceService({
    policies: POLICIES,
    store: new InMemoryStore(),
    nowMs: () => NOW,
  });
}

function seedRecord(
  service: RetentionGovernanceService,
  id: string,
  category: DataCategory,
  ageDays: number,
): void {
  const createdAtMs = NOW - ageDays * DAY;
  service.addRecord({ id, category, createdAtMs });
  service.addReplica({
    id: `${id}-replica`,
    ownerKind: "record",
    ownerId: id,
    location: `s3://backups/${id}`,
  });
  service.addIndex({
    id: `${id}-index`,
    ownerKind: "record",
    ownerId: id,
    indexName: `${category}-lookup`,
  });
}

describe("retention governance", () => {
  it("computes expiry per data category", () => {
    const service = makeService();
    const createdAt = NOW - 3 * DAY;

    assert.equal(service.expiryFor("metrics", createdAt), createdAt + 7 * DAY);
    assert.equal(service.expiryFor("logs", createdAt), createdAt + 30 * DAY);
    assert.equal(service.expiryFor("events", createdAt), createdAt + 1 * DAY);
  });

  it("deletes expired data and its derived replicas and indexes", () => {
    const service = makeService();
    seedRecord(service, "expired-metrics", "metrics", 10);
    seedRecord(service, "expired-events", "events", 5);

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedRecordIds.sort(), [
      "expired-events",
      "expired-metrics",
    ]);
    assert.equal(result.deletedArchiveIds.length, 0);

    const store = service.getStore();
    assert.equal(store.getRecord("expired-metrics"), undefined);
    assert.equal(store.getRecord("expired-events"), undefined);
    assert.equal(store.getReplica("expired-metrics-replica"), undefined);
    assert.equal(store.getIndex("expired-metrics-index"), undefined);
    assert.equal(store.getReplica("expired-events-replica"), undefined);
    assert.equal(store.getIndex("expired-events-index"), undefined);
  });

  it("keeps unexpired data and its derived copies untouched", () => {
    const service = makeService();
    seedRecord(service, "fresh-logs", "logs", 3);
    seedRecord(service, "fresh-metrics", "metrics", 2);

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedRecordIds, []);
    assert.deepEqual(result.auditEntries, []);

    const store = service.getStore();
    assert.ok(store.getRecord("fresh-logs"));
    assert.ok(store.getRecord("fresh-metrics"));
    assert.ok(store.getReplica("fresh-logs-replica"));
    assert.ok(store.getIndex("fresh-logs-index"));
    assert.ok(store.getReplica("fresh-metrics-replica"));
    assert.ok(store.getIndex("fresh-metrics-index"));
  });

  it("only deletes expired data while retaining unexpired data in the same sweep", () => {
    const service = makeService();
    seedRecord(service, "expired-metrics", "metrics", 20);
    seedRecord(service, "fresh-logs", "logs", 10);

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedRecordIds, ["expired-metrics"]);
    assert.ok(service.getStore().getRecord("fresh-logs"));
    assert.ok(service.getStore().getReplica("fresh-logs-replica"));
    assert.ok(service.getStore().getIndex("fresh-logs-index"));
    assert.equal(service.getStore().getRecord("expired-metrics"), undefined);
    assert.equal(
      service.getStore().getReplica("expired-metrics-replica"),
      undefined,
    );
    assert.equal(
      service.getStore().getIndex("expired-metrics-index"),
      undefined,
    );
  });

  it("treats expiry exactly at sweep time as expired", () => {
    const service = makeService();
    service.addRecord({
      id: "boundary-events",
      category: "events",
      createdAtMs: NOW - DAY,
    });

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedRecordIds, ["boundary-events"]);
  });

  it("rejects merging into an archive that would extend retention", () => {
    const service = makeService();
    service.addRecord({
      id: "old-events",
      category: "events",
      createdAtMs: NOW - 10 * DAY,
    });
    service.addRecord({
      id: "new-logs",
      category: "logs",
      createdAtMs: NOW - DAY,
    });

    assert.throws(
      () =>
        service.mergeArchive({
          id: "archive-extend",
          sourceIds: ["old-events", "new-logs"],
          category: "logs",
          createdAtMs: NOW,
        }),
      RetentionExtensionError,
    );

    const store = service.getStore();
    assert.equal(store.getArchive("archive-extend"), undefined);
    assert.equal(store.getRecord("old-events")?.archivedIn, undefined);
    assert.equal(store.getRecord("new-logs")?.archivedIn, undefined);
  });

  it("allows a merge whose expiry never outlives its earliest source record", () => {
    const service = makeService();
    service.addRecord({
      id: "old-logs-a",
      category: "logs",
      createdAtMs: NOW - 20 * DAY,
    });
    service.addRecord({
      id: "old-logs-b",
      category: "logs",
      createdAtMs: NOW - 5 * DAY,
    });

    const archive = service.mergeArchive({
      id: "archive-safe",
      sourceIds: ["old-logs-a", "old-logs-b"],
      createdAtMs: NOW,
    });

    const earliestExpiry =
      service.expiryFor("logs", NOW - 20 * DAY);
    assert.equal(archive.effectiveExpiryMs, earliestExpiry);
    assert.ok(archive.proposedExpiryMs <= archive.effectiveExpiryMs);
    assert.equal(
      service.getStore().getRecord("old-logs-a")?.archivedIn,
      "archive-safe",
    );
  });

  it("deletes an expired archive together with its copies, indexes and expired sources", () => {
    const service = makeService();
    service.addRecord({
      id: "archived-old",
      category: "events",
      createdAtMs: NOW - 10 * DAY,
    });
    service.addRecord({
      id: "archived-fresh",
      category: "logs",
      createdAtMs: NOW - DAY,
    });

    const archive = service.mergeArchive({
      id: "archive-due",
      sourceIds: ["archived-old", "archived-fresh"],
      createdAtMs: NOW - 2 * DAY,
    });
    service.addReplica({
      id: "archive-replica",
      ownerKind: "archive",
      ownerId: archive.id,
      location: "s3://cold/archive-due",
    });
    service.addIndex({
      id: "archive-index",
      ownerKind: "archive",
      ownerId: archive.id,
      indexName: "archive-lookup",
    });

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedArchiveIds, ["archive-due"]);
    assert.deepEqual(result.deletedRecordIds, ["archived-old"]);

    const store = service.getStore();
    assert.equal(store.getArchive("archive-due"), undefined);
    assert.equal(store.getReplica("archive-replica"), undefined);
    assert.equal(store.getIndex("archive-index"), undefined);
    assert.equal(store.getRecord("archived-old"), undefined);

    const surviving = store.getRecord("archived-fresh");
    assert.ok(surviving);
    assert.equal(surviving?.archivedIn, undefined);
  });

  it("retains an archive whose effective expiry has not been reached", () => {
    const service = makeService();
    service.addRecord({
      id: "recent-logs",
      category: "logs",
      createdAtMs: NOW - DAY,
    });

    service.mergeArchive({
      id: "archive-young",
      sourceIds: ["recent-logs"],
      createdAtMs: NOW,
    });

    const result = service.runRetentionSweep();

    assert.deepEqual(result.deletedArchiveIds, []);
    assert.deepEqual(result.deletedRecordIds, []);
    assert.ok(service.getStore().getArchive("archive-young"));
    assert.ok(service.getStore().getRecord("recent-logs"));
  });

  it("writes append-only deletion audit records for every deleted target", () => {
    const service = makeService();
    seedRecord(service, "expired-metrics", "metrics", 15);
    service.addRecord({
      id: "fresh-logs",
      category: "logs",
      createdAtMs: NOW - DAY,
    });

    const result = service.runRetentionSweep();
    const auditLog = service.getStore().getAuditLog();

    assert.equal(auditLog.length, 1);
    assert.equal(result.auditEntries[0], auditLog[0]);

    const entry = auditLog[0];
    assert.equal(entry.targetKind, "record");
    assert.equal(entry.targetId, "expired-metrics");
    assert.equal(entry.category, "metrics");
    assert.equal(entry.reason, "retention_expired");
    assert.equal(entry.deletedAtMs, NOW);
    assert.equal(
      entry.expiryMs,
      service.expiryFor("metrics", NOW - 15 * DAY),
    );
    assert.deepEqual(entry.deletedReplicaIds, ["expired-metrics-replica"]);
    assert.deepEqual(entry.deletedIndexIds, ["expired-metrics-index"]);

    const secondSweep = service.runRetentionSweep();
    assert.deepEqual(secondSweep.deletedRecordIds, []);
    assert.equal(service.getStore().getAuditLog().length, 1);
  });

  it("refuses records whose category has no retention policy", () => {
    const service = makeService();

    assert.throws(
      () =>
        service.addRecord({
          id: "unknown",
          category: "traces" as DataCategory,
          createdAtMs: NOW,
        }),
      RetentionPolicyError,
    );
  });
});
