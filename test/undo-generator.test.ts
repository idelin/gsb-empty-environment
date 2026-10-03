import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { CdcLog } from "../src/cdc-log.ts";
import { generateRollbackPackage } from "../src/undo-generator.ts";

const base = { tsMs: 1000, txId: "tx-1", table: "users" };

describe("generateRollbackPackage", () => {
  it("inverts UPDATE back to the before image", () => {
    const log = new CdcLog();
    log.append({
      ...base,
      op: "UPDATE",
      pk: 7,
      before: { id: 7, name: "Alice", balance: 100 },
      after: { id: 7, name: "Alice", balance: 0 },
    });
    const pkg = generateRollbackPackage(log.all());
    assert.equal(pkg.ops.length, 1);
    const op = pkg.ops[0];
    assert.equal(op.op, "UPDATE");
    assert.deepEqual(op.image, { id: 7, name: "Alice", balance: 100 });
    assert.equal(
      op.sql,
      `UPDATE "users" SET "name" = 'Alice', "balance" = 100 WHERE "id" = 7;`,
    );
  });

  it("inverts INSERT into DELETE and DELETE into INSERT of before image", () => {
    const log = new CdcLog();
    log.append({ ...base, op: "INSERT", pk: 1, before: null, after: { id: 1, name: "A" } });
    log.append({ ...base, op: "DELETE", pk: 2, before: { id: 2, name: "B" }, after: null });
    const pkg = generateRollbackPackage(log.all());
    // reverse commit order: DELETE event compensated first
    assert.equal(pkg.ops[0].op, "INSERT");
    assert.equal(pkg.ops[0].sql, `INSERT INTO "users" ("id", "name") VALUES (2, 'B');`);
    assert.equal(pkg.ops[1].op, "DELETE");
    assert.equal(pkg.ops[1].sql, `DELETE FROM "users" WHERE "id" = 1;`);
  });

  it("compensates chained updates to the same row in reverse order", () => {
    const log = new CdcLog();
    log.append({ ...base, op: "UPDATE", pk: 1, before: { id: 1, v: 1 }, after: { id: 1, v: 2 } });
    log.append({ ...base, op: "UPDATE", pk: 1, before: { id: 1, v: 2 }, after: { id: 1, v: 3 } });
    const pkg = generateRollbackPackage(log.all());
    assert.deepEqual(pkg.ops.map((o) => o.image), [{ id: 1, v: 2 }, { id: 1, v: 1 }]);
    assert.deepEqual(pkg.coveredSeq, { from: 1, to: 2 });
  });

  it("escapes single quotes in SQL literals", () => {
    const log = new CdcLog();
    log.append({
      ...base,
      op: "UPDATE",
      pk: 1,
      before: { id: 1, name: "O'Brien" },
      after: { id: 1, name: "" },
    });
    const pkg = generateRollbackPackage(log.all());
    assert.match(pkg.ops[0].sql, /'O''Brien'/);
  });

  it("throws when a before image is missing", () => {
    const log = new CdcLog();
    log.append({ ...base, op: "UPDATE", pk: 1, before: null, after: { id: 1 } });
    assert.throws(() => generateRollbackPackage(log.all()), /missing before image/);
  });
});
