import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MigrationEngine, type PublishRecord } from "../src/engine.ts";
import { MigrationError } from "../src/errors.ts";
import { VersionGraph, type MigrationStep } from "../src/graph.ts";
import { FileJournalStore, MemoryJournalStore } from "../src/journal.ts";
import type { JsonObject, JsonValue } from "../src/json.ts";

const obj = (v: JsonValue): JsonObject => v as JsonObject;

function catchMigration(fn: () => unknown): MigrationError {
  try {
    fn();
  } catch (e) {
    assert.ok(e instanceof MigrationError, "expected MigrationError");
    return e;
  }
  assert.fail("expected function to throw");
}

const v1Schema = {
  type: "object",
  properties: { name: { type: "string" } },
  required: ["name"],
} as const;
const v2Schema = {
  type: "object",
  properties: {
    name: { type: "string" },
    greeting: { type: "string" },
    log: { type: "array", items: { type: "string" } },
  },
  required: ["name", "greeting", "log"],
} as const;
const v3Schema = v2Schema;

interface Flags {
  crashOnShout: boolean;
}

function makeGraph(flags: Flags = { crashOnShout: false }): VersionGraph {
  const g = new VersionGraph();
  const steps: MigrationStep[] = [
    {
      name: "add-greeting",
      from: "v1",
      to: "v2",
      inputSchema: v1Schema,
      outputSchema: v2Schema,
      apply(doc) {
        const d = obj(doc);
        const log = (d.log as JsonValue[] | undefined) ?? [];
        return { ...d, greeting: `hello ${d.name}`, log: [...log, "v2"] };
      },
    },
    {
      name: "shout",
      from: "v2",
      to: "v3",
      inputSchema: v2Schema,
      outputSchema: v3Schema,
      apply(doc) {
        if (flags.crashOnShout) {
          flags.crashOnShout = false;
          throw new Error("boom");
        }
        const d = obj(doc);
        return {
          ...d,
          greeting: String(d.greeting).toUpperCase(),
          log: [...(d.log as JsonValue[]), "v3"],
        };
      },
    },
  ];
  for (const s of steps) g.addStep(s);
  return g;
}

test("happy path: runs all steps and publishes exactly once", () => {
  const published: PublishRecord[] = [];
  const engine = new MigrationEngine();
  const result = engine.migrate({
    graph: makeGraph(),
    from: "v1",
    to: "v3",
    input: { name: "ada" },
    publish: (r) => published.push(r),
  });
  assert.equal(result.version, "v3");
  assert.deepEqual(result.executedSteps, ["add-greeting", "shout"]);
  assert.deepEqual(obj(result.data).log, ["v2", "v3"]);
  assert.equal(obj(result.data).greeting, "HELLO ADA");
  assert.equal(published.length, 1);
  assert.equal(published[0].version, "v3");
});

test("input schema violation: error located at path, nothing published", () => {
  const published: PublishRecord[] = [];
  const engine = new MigrationEngine();
  const err = catchMigration(() =>
    engine.migrate({
      graph: makeGraph(),
      from: "v1",
      to: "v3",
      input: { name: 42 },
      publish: (r) => published.push(r),
    }),
  );
  assert.equal(err.code, "SCHEMA_INPUT");
  assert.equal(err.step, "add-greeting");
  assert.equal(err.path, "$.name");
  assert.equal(published.length, 0);
});

test("missing required field is located at its path", () => {
  const engine = new MigrationEngine();
  const err = catchMigration(() =>
    engine.migrate({ graph: makeGraph(), from: "v1", to: "v3", input: {} }),
  );
  assert.equal(err.code, "SCHEMA_INPUT");
  assert.equal(err.path, "$.name");
});

test("throwing step: STEP_APPLY with step name, nothing published", () => {
  const published: PublishRecord[] = [];
  const engine = new MigrationEngine();
  const err = catchMigration(() =>
    engine.migrate({
      graph: makeGraph({ crashOnShout: true }),
      from: "v1",
      to: "v3",
      input: { name: "ada" },
      publish: (r) => published.push(r),
    }),
  );
  assert.equal(err.code, "STEP_APPLY");
  assert.equal(err.step, "shout");
  assert.equal(published.length, 0);
});

test("output schema violation: SCHEMA_OUTPUT, nothing published", () => {
  const g = new VersionGraph();
  g.addStep({
    name: "bad-output",
    from: "v1",
    to: "v2",
    inputSchema: { type: "any" },
    outputSchema: v2Schema,
    apply: () => ({ name: "ada" }), // missing greeting/log
  });
  const published: PublishRecord[] = [];
  const engine = new MigrationEngine();
  const err = catchMigration(() =>
    engine.migrate({
      graph: g,
      from: "v1",
      to: "v2",
      input: { name: "ada" },
      publish: (r) => published.push(r),
    }),
  );
  assert.equal(err.code, "SCHEMA_OUTPUT");
  assert.equal(err.step, "bad-output");
  assert.equal(err.path, "$.greeting");
  assert.equal(published.length, 0);
});

test("non-JSON input is rejected with a path", () => {
  const engine = new MigrationEngine();
  const err = catchMigration(() =>
    engine.migrate({
      graph: makeGraph(),
      from: "v1",
      to: "v3",
      input: { name: "ada", score: NaN },
    }),
  );
  assert.equal(err.code, "INVALID_JSON_TREE");
  assert.equal(err.path, "$.score");
});

test("crash + resume: completed steps are skipped, fields not duplicated", () => {
  const flags = { crashOnShout: true };
  const graph = makeGraph(flags);
  const journal = new MemoryJournalStore();
  const engine = new MigrationEngine();
  const input: JsonValue = { name: "grace" };

  assert.throws(() =>
    engine.migrate({ graph, from: "v1", to: "v3", input, journal }),
  );
  const saved = journal.load();
  assert.ok(saved);
  assert.deepEqual(saved.completedSteps, ["add-greeting"]);
  assert.deepEqual(obj(saved.state).log, ["v2"]);

  const result = engine.migrate({ graph, from: "v1", to: "v3", input, journal });
  assert.deepEqual(result.skippedSteps, ["add-greeting"]);
  assert.deepEqual(result.executedSteps, ["shout"]);
  // "v2" was appended by the first (crashed) run; replay must not re-append.
  assert.deepEqual(obj(result.data).log, ["v2", "v3"]);
  assert.equal(journal.load(), null, "journal cleared after success");
});

test("resume accepts the same input with different key order", () => {
  const flags = { crashOnShout: true };
  const graph = makeGraph(flags);
  const journal = new MemoryJournalStore();
  const engine = new MigrationEngine();
  assert.throws(() =>
    engine.migrate({
      graph,
      from: "v1",
      to: "v3",
      input: { name: "grace", extra: 1 },
      journal,
    }),
  );
  const result = engine.migrate({
    graph,
    from: "v1",
    to: "v3",
    input: { extra: 1, name: "grace" }, // same tree, different key order
    journal,
  });
  assert.equal(result.version, "v3");
});

test("resume rejects a tampered original input", () => {
  const flags = { crashOnShout: true };
  const graph = makeGraph(flags);
  const journal = new MemoryJournalStore();
  const engine = new MigrationEngine();
  assert.throws(() =>
    engine.migrate({ graph, from: "v1", to: "v3", input: { name: "grace" }, journal }),
  );
  const err = catchMigration(() =>
    engine.migrate({
      graph,
      from: "v1",
      to: "v3",
      input: { name: "mallory" },
      journal,
    }),
  );
  assert.equal(err.code, "CHECKPOINT_INPUT_MISMATCH");
});

test("resume rejects a journal whose steps are not a prefix of the plan", () => {
  const flags = { crashOnShout: true };
  const graph = makeGraph(flags);
  const journal = new MemoryJournalStore();
  const engine = new MigrationEngine();
  assert.throws(() =>
    engine.migrate({ graph, from: "v1", to: "v3", input: { name: "grace" }, journal }),
  );
  // A different graph whose first step has another name.
  const other = new VersionGraph();
  other.addStep({
    name: "renamed-step",
    from: "v1",
    to: "v2",
    inputSchema: { type: "any" },
    outputSchema: { type: "any" },
    apply: (d) => d,
  });
  other.addStep({
    name: "shout",
    from: "v2",
    to: "v3",
    inputSchema: { type: "any" },
    outputSchema: { type: "any" },
    apply: (d) => d,
  });
  const err = catchMigration(() =>
    engine.migrate({ graph: other, from: "v1", to: "v3", input: { name: "grace" }, journal }),
  );
  assert.equal(err.code, "CHECKPOINT_PLAN_MISMATCH");
});

test("file journal persists checkpoints across engine instances", () => {
  const dir = mkdtempSync(join(tmpdir(), "migration-journal-"));
  const file = join(dir, "journal.json");
  const flags = { crashOnShout: true };
  const graph = makeGraph(flags);
  const input: JsonValue = { name: "ada" };

  assert.throws(() =>
    new MigrationEngine().migrate({
      graph,
      from: "v1",
      to: "v3",
      input,
      journal: new FileJournalStore(file),
    }),
  );
  assert.ok(existsSync(file));
  const onDisk = JSON.parse(readFileSync(file, "utf8"));
  assert.deepEqual(onDisk.completedSteps, ["add-greeting"]);

  const result = new MigrationEngine().migrate({
    graph,
    from: "v1",
    to: "v3",
    input,
    journal: new FileJournalStore(file),
  });
  assert.deepEqual(result.skippedSteps, ["add-greeting"]);
  assert.deepEqual(obj(result.data).log, ["v2", "v3"]);
  assert.ok(!existsSync(file), "journal file removed after success");
});
