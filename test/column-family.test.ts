import assert from "node:assert/strict";
import { appendFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, it } from "node:test";
import { Engine } from "../src/engine.ts";

let dir: string;
let engine: Engine;

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), "lsm-cf-"));
  engine = Engine.open(dir);
  engine.createColumnFamily("CF_Default");
  engine.createColumnFamily("CF_Metadata");
});

afterEach(() => {
  engine.close();
  rmSync(dir, { recursive: true, force: true });
});

describe("column family management", () => {
  it("creates CF_Default and CF_Metadata in one instance", () => {
    assert.deepEqual(engine.listColumnFamilies().sort(), ["CF_Default", "CF_Metadata"]);
  });

  it("rejects duplicate column family names", () => {
    assert.throws(() => engine.createColumnFamily("CF_Default"), /exists/);
  });

  it("rejects writes to unknown column families", () => {
    assert.throws(() => engine.put("CF_Nope", "k", "v"), /unknown column family/);
  });
});

describe("atomic multi-CF writes", () => {
  it("commits one batch across both families atomically", () => {
    const seq = engine.writeBatch([
      { cf: "CF_Default", key: "user:1", value: "alice" },
      { cf: "CF_Metadata", key: "schema:users", value: "v3" },
    ]);
    assert.equal(engine.get("CF_Default", "user:1"), "alice");
    assert.equal(engine.get("CF_Metadata", "schema:users"), "v3");
    assert.equal(engine.snapshot("CF_Default").seq, seq);
    assert.equal(engine.snapshot("CF_Metadata").seq, seq);
  });

  it("validates all CFs before logging anything", () => {
    assert.throws(() =>
      engine.writeBatch([
        { cf: "CF_Default", key: "a", value: "1" },
        { cf: "CF_Ghost", key: "b", value: "2" },
      ]),
    );
    assert.equal(engine.get("CF_Default", "a"), undefined); // nothing applied
  });
});

describe("isolation between families", () => {
  it("CF_Metadata only sees its own data", () => {
    engine.writeBatch([
      { cf: "CF_Default", key: "shared-key", value: "from-default" },
      { cf: "CF_Metadata", key: "shared-key", value: "from-metadata" },
      { cf: "CF_Default", key: "default-only", value: "d" },
    ]);
    assert.equal(engine.get("CF_Metadata", "shared-key"), "from-metadata");
    assert.equal(engine.get("CF_Default", "shared-key"), "from-default");
    assert.equal(engine.get("CF_Metadata", "default-only"), undefined);
    assert.deepEqual(engine.keys("CF_Metadata"), ["shared-key"]);
    assert.deepEqual(engine.keys("CF_Default"), ["default-only", "shared-key"]);
  });

  it("keeps independent MemTables and SSTable metadata per CF", () => {
    engine.put("CF_Default", "k1", "v1");
    engine.flush("CF_Default");
    assert.equal(engine.columnFamily("CF_Default").sstableCount, 1);
    assert.equal(engine.columnFamily("CF_Metadata").sstableCount, 0);
    assert.equal(engine.columnFamily("CF_Default").memtableSize, 0);
    assert.equal(engine.get("CF_Default", "k1"), "v1"); // served from SSTable
  });
});

describe("independent snapshots", () => {
  it("pins one CF at a fixed view while writes continue", () => {
    engine.put("CF_Default", "k", "v1");
    const snap = engine.snapshot("CF_Default");
    engine.put("CF_Default", "k", "v2");
    engine.put("CF_Default", "new-key", "x");
    assert.equal(snap.get("k"), "v1");
    assert.equal(snap.get("new-key"), undefined);
    assert.equal(engine.get("CF_Default", "k"), "v2");
  });

  it("snapshot of CF_Metadata is unaffected by CF_Default writes", () => {
    engine.put("CF_Metadata", "m", "1");
    const snap = engine.snapshot("CF_Metadata");
    engine.put("CF_Default", "noise", "n");
    engine.put("CF_Metadata", "m", "2");
    assert.equal(snap.get("m"), "1");
    assert.deepEqual(snap.keys(), ["m"]);
  });
});

describe("recovery from the shared WAL", () => {
  it("replays both families atomically aligned after reopen", () => {
    engine.writeBatch([
      { cf: "CF_Default", key: "tx-key", value: "data" },
      { cf: "CF_Metadata", key: "tx-key", value: "meta" },
    ]);
    engine.put("CF_Default", "solo", "s");
    engine.close();

    const reopened = Engine.open(dir);
    assert.deepEqual(reopened.listColumnFamilies().sort(), ["CF_Default", "CF_Metadata"]);
    // the multi-CF batch is visible in BOTH families or neither — never half
    assert.equal(reopened.get("CF_Default", "tx-key"), "data");
    assert.equal(reopened.get("CF_Metadata", "tx-key"), "meta");
    assert.equal(reopened.get("CF_Default", "solo"), "s");
    reopened.close();
    engine = Engine.open(dir); // keep afterEach cleanup valid
  });

  it("recovers from SSTables + WAL after flush", () => {
    engine.put("CF_Default", "old", "flushed");
    engine.flush("CF_Default");
    engine.put("CF_Default", "new", "in-wal");
    engine.close();

    const reopened = Engine.open(dir);
    assert.equal(reopened.columnFamily("CF_Default").sstableCount, 1);
    assert.equal(reopened.get("CF_Default", "old"), "flushed");
    assert.equal(reopened.get("CF_Default", "new"), "in-wal");
    reopened.close();
    engine = Engine.open(dir);
  });

  it("ignores a torn tail record, keeping cross-CF alignment", () => {
    engine.writeBatch([
      { cf: "CF_Default", key: "committed", value: "yes" },
      { cf: "CF_Metadata", key: "committed", value: "yes" },
    ]);
    engine.close();
    // simulate a crash mid-write: partial batch line at the WAL tail
    appendFileSync(join(dir, "LOG.wal"), '42:deadbeef:{"seq":99,"ops":[{"cf":"CF_Default","ke');

    const reopened = Engine.open(dir);
    assert.equal(reopened.get("CF_Default", "committed"), "yes");
    assert.equal(reopened.get("CF_Metadata", "committed"), "yes");
    assert.equal(reopened.get("CF_Default", "ke"), undefined); // torn batch dropped entirely
    reopened.close();
    engine = Engine.open(dir);
  });
});
