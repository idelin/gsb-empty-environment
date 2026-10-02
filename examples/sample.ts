/**
 * Runnable demo: npm run sample
 *
 * Walks through: versioned schemas, atomic batches with mutual references,
 * cycles, in-transaction rename, reverse cardinality, delete protection,
 * and error reports with instance path + rule path + minimal slice.
 */
import { SchemaRegistry } from "../src/schema.ts";
import { Repository, type Document } from "../src/repository.ts";
import { validateAll } from "../src/validator.ts";
import { formatError, type ValidationError } from "../src/errors.ts";

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  fn();
}

function showErrors(errors: ValidationError[]): void {
  for (const err of errors) {
    console.log("  " + formatError(err));
    console.log(`    slice: ${JSON.stringify(err.slice)}`);
  }
}

const schemas = new SchemaRegistry()
  .register({
    version: "v1",
    types: {
      User: { fields: { name: { kind: "string", required: true } } },
      Task: {
        fields: {
          title: { kind: "string", required: true },
          assignee: { kind: "ref", target: "User" },
          watchers: { kind: "ref[]", target: "User" },
        },
      },
    },
  })
  .register({
    version: "v2", // v2: Task.assignee now targets Account
    types: {
      Account: { fields: { name: { kind: "string", required: true } } },
      Task: {
        fields: {
          title: { kind: "string", required: true },
          assignee: { kind: "ref", target: "Account" },
        },
      },
    },
  });

const repo = new Repository(schemas);

show("1. Atomic batch: mutually referencing docs inserted together", () => {
  const res = repo.batch((tx) => {
    tx.put({ id: "u1", type: "User", schemaVersion: "v1", data: { name: "Ada" } });
    tx.put({ id: "u2", type: "User", schemaVersion: "v1", data: { name: "Grace" } });
    tx.put({
      id: "t1",
      type: "Task",
      schemaVersion: "v1",
      data: { title: "write compiler", assignee: "u1", watchers: ["u2"] },
    });
    tx.put({
      id: "t2",
      type: "Task",
      schemaVersion: "v1",
      data: { title: "review", assignee: "u2", watchers: ["u1"] },
    });
  });
  console.log("  commit:", JSON.stringify(res));
  console.log(`  reverse index: u1 <- ${repo.incomingCount("u1")} refs, u2 <- ${repo.incomingCount("u2")} refs`);
});

show("2. Same-batch duplicate id with different content conflicts (atomic rollback)", () => {
  const res = repo.batch((tx) => {
    tx.put({ id: "u3", type: "User", schemaVersion: "v1", data: { name: "one" } });
    tx.put({ id: "u3", type: "User", schemaVersion: "v1", data: { name: "two" } });
  });
  if (!res.ok) showErrors(res.errors);
  console.log(`  repo still has ${repo.size} docs (u3 not inserted)`);
});

show("3. Rename inside a transaction rewrites all references", () => {
  const res = repo.batch((tx) => tx.rename("u1", "user-ada"));
  console.log("  commit:", JSON.stringify(res));
  console.log("  t1.data.assignee =", repo.get("t1")?.data.assignee);
  console.log("  t2.data.watchers =", JSON.stringify(repo.get("t2")?.data.watchers));
});

show("4. Deleting a still-referenced document is rejected", () => {
  const res = repo.batch((tx) => tx.delete("user-ada"));
  if (!res.ok) showErrors(res.errors);
});

show("5. Versioned schema decides the target type", () => {
  const bad: Document = {
    id: "t3",
    type: "Task",
    schemaVersion: "v2",
    data: { title: "migrate", assignee: "u2" }, // u2 is a User, v2 wants Account
  };
  const res = repo.batch((tx) => tx.put(bad));
  if (!res.ok) showErrors(res.errors);
  const ok = repo.batch((tx) => {
    tx.put({ id: "ac1", type: "Account", schemaVersion: "v2", data: { name: "team" } });
    tx.put({ id: "t3", type: "Task", schemaVersion: "v2", data: { title: "migrate", assignee: "ac1" } });
  });
  console.log("  with an Account target:", JSON.stringify(ok));
});

show("6. Dangling reference error: instance path + rule path + minimal slice", () => {
  const res = repo.batch((tx) =>
    tx.put({ id: "t9", type: "Task", schemaVersion: "v1", data: { title: "x", assignee: "ghost" } }),
  );
  if (!res.ok) showErrors(res.errors);
});

show("7. Cycles are legal; deleting entry points leaves zero residual errors", () => {
  const cyc = new Repository(schemas);
  console.log(
    "  insert a<->b cycle:",
    JSON.stringify(
      cyc.batch((tx) => {
        tx.put({ id: "u1", type: "User", schemaVersion: "v1", data: { name: "a" } });
        tx.put({ id: "t1", type: "Task", schemaVersion: "v1", data: { title: "t1", assignee: "u1", watchers: ["u2"] } });
        tx.put({ id: "u2", type: "User", schemaVersion: "v1", data: { name: "b" } });
        tx.put({ id: "t2", type: "Task", schemaVersion: "v1", data: { title: "t2", assignee: "u2", watchers: ["u1"] } });
      }),
    ),
  );
  console.log(
    "  delete all entry points:",
    JSON.stringify(
      cyc.batch((tx) => {
        tx.delete("t1");
        tx.delete("t2");
        tx.delete("u1");
        tx.delete("u2");
      }),
    ),
  );
  const full = validateAll(cyc.all(), schemas);
  console.log(`  residual errors after teardown: ${full.errors.length}, repo size: ${cyc.size}`);
});

show("8. Independent full validator agrees with the repository", () => {
  const full = validateAll(repo.all(), schemas);
  console.log(`  full validation of live repo: ${full.errors.length} error(s), ${repo.size} docs`);
});
