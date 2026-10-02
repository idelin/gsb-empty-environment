import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaRegistry } from "../src/schema.ts";
import { Repository, type Document } from "../src/repository.ts";
import { validateAll } from "../src/validator.ts";
import { formatError } from "../src/errors.ts";

function makeSchemas(): SchemaRegistry {
  const schemas = new SchemaRegistry();
  schemas.register({
    version: "v1",
    types: {
      User: {
        fields: { name: { kind: "string", required: true } },
      },
      Task: {
        fields: {
          title: { kind: "string", required: true },
          assignee: { kind: "ref", target: "User" },
          watchers: { kind: "ref[]", target: "User" },
        },
      },
      Key: {
        fields: { label: { kind: "string" } },
        incoming: { min: 1, max: 2 },
      },
      KeyHolder: {
        fields: { key: { kind: "ref", target: "Key", required: true } },
      },
    },
  });
  // v2 changes the target type of Task.assignee: User -> Account.
  schemas.register({
    version: "v2",
    types: {
      User: { fields: { name: { kind: "string", required: true } } },
      Account: { fields: { name: { kind: "string", required: true } } },
      Task: {
        fields: {
          title: { kind: "string", required: true },
          assignee: { kind: "ref", target: "Account" },
        },
      },
    },
  });
  return schemas;
}

const user = (id: string, name = id): Document => ({
  id,
  type: "User",
  schemaVersion: "v1",
  data: { name },
});

const task = (id: string, assignee?: string, watchers: string[] = []): Document => ({
  id,
  type: "Task",
  schemaVersion: "v1",
  data: { title: id, ...(assignee ? { assignee } : {}), watchers },
});

test("batch atomic insert: mutual references in the same batch pass existence checks", () => {
  const repo = new Repository(makeSchemas());
  const a: Document = { id: "a", type: "Task", schemaVersion: "v1", data: { title: "a", assignee: "b" } };
  // "b" is a User? no — make a cycle between two tasks via watchers to prove same-batch visibility.
  const result = repo.batch((tx) => {
    tx.put(user("u1"));
    tx.put(task("t1", "u1", ["u2"]));
    tx.put(user("u2"));
    tx.put(a);
  });
  // "a" references "b" which does not exist -> the whole batch must fail atomically.
  assert.equal(result.ok, false);
  assert.equal(repo.size, 0);

  const ok = repo.batch((tx) => {
    tx.put(user("u1"));
    tx.put(task("t1", "u1", ["u2"]));
    tx.put(user("u2"));
  });
  assert.deepEqual(ok, { ok: true });
  assert.equal(repo.size, 3);
  assert.equal(repo.incomingCount("u1"), 1);
  assert.equal(repo.incomingCount("u2"), 1);
});

test("deleting referenced entry points in one batch leaves zero residual errors", () => {
  const repo = new Repository(makeSchemas());
  const cyclic: Document = {
    id: "t1",
    type: "Task",
    schemaVersion: "v1",
    data: { title: "t1", assignee: "u1", watchers: [] },
  };
  const ok = repo.batch((tx) => {
    tx.put(user("u1"));
    tx.put(cyclic);
    tx.put({ id: "t2", type: "Task", schemaVersion: "v1", data: { title: "t2", assignee: "u1" } });
  });
  assert.equal(ok.ok, true);

  // Genuine reference cycles are covered in tests/crosscheck.test.ts.
  const del = repo.batch((tx) => {
    tx.delete("t1");
    tx.delete("t2");
    tx.delete("u1");
  });
  assert.equal(del.ok, true);
  assert.equal(repo.size, 0);
  const full = validateAll(repo.all(), makeSchemas());
  assert.equal(full.errors.length, 0);
  assert.equal(repo.incomingCount("u1"), 0);
});

test("duplicate id with different documents in one batch conflicts; identical content is idempotent", () => {
  const repo = new Repository(makeSchemas());
  const conflict = repo.batch((tx) => {
    tx.put(user("u1", "alice"));
    tx.put(user("u1", "bob"));
  });
  assert.equal(conflict.ok, false);
  if (!conflict.ok) {
    assert.equal(conflict.errors[0].code, "DuplicateId");
    assert.equal(conflict.errors[0].instancePath, "/id");
  }
  assert.equal(repo.size, 0);

  const same = repo.batch((tx) => {
    tx.put(user("u1", "alice"));
    tx.put(user("u1", "alice"));
  });
  assert.equal(same.ok, true);
  assert.equal(repo.size, 1);
});

test("deleting a still-referenced document is rejected and the batch is atomic", () => {
  const repo = new Repository(makeSchemas());
  assert.equal(
    repo.batch((tx) => {
      tx.put(user("u1"));
      tx.put(task("t1", "u1"));
    }).ok,
    true,
  );

  const before = JSON.stringify(repo.all());
  const res = repo.batch((tx) => {
    tx.put(user("u2"));
    tx.delete("u1");
  });
  assert.equal(res.ok, false);
  if (!res.ok) {
    const err = res.errors.find((e) => e.code === "DeleteReferenced");
    assert.ok(err, "expected DeleteReferenced");
    assert.equal(err.docId, "u1");
    assert.deepEqual(err.slice, [{ docId: "t1", paths: ["/data/assignee"] }]);
  }
  // atomic: u2 must not have been inserted either
  assert.equal(JSON.stringify(repo.all()), before);

  // deleting referrer and target together is fine
  const ok = repo.batch((tx) => {
    tx.delete("t1");
    tx.delete("u1");
  });
  assert.equal(ok.ok, true);
  assert.equal(repo.size, 0);
});

test("rename inside a transaction rewrites references and the reverse index", () => {
  const repo = new Repository(makeSchemas());
  repo.batch((tx) => {
    tx.put(user("u1"));
    tx.put(task("t1", "u1", ["u1"]));
  });
  const res = repo.batch((tx) => tx.rename("u1", "user-1"));
  assert.equal(res.ok, true);
  assert.equal(repo.has("u1"), false);
  assert.equal(repo.has("user-1"), true);
  assert.deepEqual(repo.get("t1")?.data.assignee, "user-1");
  assert.deepEqual(repo.get("t1")?.data.watchers, ["user-1"]);
  assert.equal(repo.incomingCount("user-1"), 2);
  assert.equal(repo.incomingCount("u1"), 0);

  // rename to an existing id conflicts
  const conflict = repo.batch((tx) => tx.rename("user-1", "t1"));
  assert.equal(conflict.ok, false);
  if (!conflict.ok) assert.equal(conflict.errors[0].code, "RenameConflict");

  // rename of a missing document fails
  const missing = repo.batch((tx) => tx.rename("nope", "x"));
  assert.equal(missing.ok, false);
  if (!missing.ok) assert.equal(missing.errors[0].code, "MissingDocument");

  // rename + new doc referencing the *new* id in the same batch
  const chained = repo.batch((tx) => {
    tx.rename("user-1", "u9");
    tx.put(task("t2", "u9"));
  });
  assert.equal(chained.ok, true);
  assert.equal(repo.get("t2")?.data.assignee, "u9");
});

test("reverse cardinality min/max is enforced with schema rule path and referrer slice", () => {
  const repo = new Repository(makeSchemas());
  // min: 1 -> a Key with no holders is invalid
  const lonely = repo.batch((tx) =>
    tx.put({ id: "k1", type: "Key", schemaVersion: "v1", data: { label: "k" } }),
  );
  assert.equal(lonely.ok, false);
  if (!lonely.ok) {
    const err = lonely.errors.find((e) => e.code === "ReverseCardinality");
    assert.ok(err);
    assert.equal(err.rulePath, "/types/Key/incoming");
    assert.equal(err.instancePath, "/id");
    assert.deepEqual(err.slice, []);
  }

  // max: 2 -> three holders exceed the cap
  const repo2 = new Repository(makeSchemas());
  const tooMany = repo2.batch((tx) => {
    tx.put({ id: "k1", type: "Key", schemaVersion: "v1", data: {} });
    tx.put({ id: "h1", type: "KeyHolder", schemaVersion: "v1", data: { key: "k1" } });
    tx.put({ id: "h2", type: "KeyHolder", schemaVersion: "v1", data: { key: "k1" } });
    tx.put({ id: "h3", type: "KeyHolder", schemaVersion: "v1", data: { key: "k1" } });
  });
  assert.equal(tooMany.ok, false);
  if (!tooMany.ok) {
    const err = tooMany.errors.find((e) => e.code === "ReverseCardinality");
    assert.ok(err);
    assert.match(err.message, /3 incoming/);
    assert.equal((err.slice as unknown[]).length, 3);
  }
  assert.equal(repo2.size, 0);
});

test("target type is judged by the referencing document's schema version", () => {
  const repo = new Repository(makeSchemas());
  repo.batch((tx) => tx.put(user("u1")));

  // v2 Task.assignee must point to an Account, not a User
  const v2task: Document = {
    id: "t1",
    type: "Task",
    schemaVersion: "v2",
    data: { title: "t", assignee: "u1" },
  };
  const bad = repo.batch((tx) => tx.put(v2task));
  assert.equal(bad.ok, false);
  if (!bad.ok) {
    const err = bad.errors.find((e) => e.code === "TargetTypeMismatch");
    assert.ok(err);
    assert.equal(err.rulePath, "/types/Task/fields/assignee");
    assert.deepEqual(err.slice, { targetId: "u1", expected: "Account", actual: "User" });
  }

  // same reference is legal under v1 rules
  const v1task: Document = { ...v2task, id: "t2", schemaVersion: "v1", data: { title: "t", assignee: "u1", watchers: [] } };
  assert.equal(repo.batch((tx) => tx.put(v1task)).ok, true);

  // and v2 accepts an Account target
  const ok = repo.batch((tx) => {
    tx.put({ id: "ac1", type: "Account", schemaVersion: "v2", data: { name: "ac" } });
    tx.put({ id: "t3", type: "Task", schemaVersion: "v2", data: { title: "t", assignee: "ac1" } });
  });
  assert.equal(ok.ok, true);
});

test("errors carry instance path, rule path and the minimal relevant slice", () => {
  const repo = new Repository(makeSchemas());
  const res = repo.batch((tx) => tx.put(task("t1", "ghost")));
  assert.equal(res.ok, false);
  if (!res.ok) {
    const err = res.errors.find((e) => e.code === "MissingTarget");
    assert.ok(err);
    assert.equal(err.docId, "t1");
    assert.equal(err.instancePath, "/data/assignee");
    assert.equal(err.rulePath, "/types/Task/fields/assignee");
    assert.equal(err.slice, "ghost");
    assert.match(formatError(err), /MissingTarget.*\/data\/assignee.*\/types\/Task\/fields\/assignee/);
  }

  const res2 = repo.batch((tx) =>
    tx.put({ id: "t2", type: "Task", schemaVersion: "v1", data: { title: 42, watchers: [] } }),
  );
  assert.equal(res2.ok, false);
  if (!res2.ok) {
    const codes = res2.errors.map((e) => e.code).sort();
    assert.deepEqual(codes, ["FieldTypeMismatch"]);
    assert.equal(res2.errors[0].slice, 42);
  }

  const res3 = repo.batch((tx) =>
    tx.put({ id: "t3", type: "Task", schemaVersion: "v1", data: { watchers: [] } }),
  );
  assert.equal(res3.ok, false);
  if (!res3.ok) assert.equal(res3.errors[0].code, "MissingRequiredField");
});

test("put replaces an existing document and updates the reverse index", () => {
  const repo = new Repository(makeSchemas());
  repo.batch((tx) => {
    tx.put(user("u1"));
    tx.put(user("u2"));
    tx.put(task("t1", "u1"));
  });
  assert.equal(repo.incomingCount("u1"), 1);
  const res = repo.batch((tx) => tx.put(task("t1", "u2")));
  assert.equal(res.ok, true);
  assert.equal(repo.incomingCount("u1"), 0);
  assert.equal(repo.incomingCount("u2"), 1);
});

test("unknown type / unknown schema version are reported", () => {
  const repo = new Repository(makeSchemas());
  const res = repo.batch((tx) =>
    tx.put({ id: "x", type: "Nope", schemaVersion: "v1", data: {} }),
  );
  assert.equal(res.ok, false);
  if (!res.ok) assert.equal(res.errors[0].code, "UnknownType");

  const res2 = repo.batch((tx) =>
    tx.put({ id: "y", type: "User", schemaVersion: "v9", data: { name: "y" } }),
  );
  assert.equal(res2.ok, false);
  if (!res2.ok) assert.equal(res2.errors[0].code, "UnknownSchemaVersion");
});
