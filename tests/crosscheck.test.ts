import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaRegistry } from "../src/schema.ts";
import { Repository, type Document } from "../src/repository.ts";
import { validateAll } from "../src/validator.ts";

/** Schema with a genuine reference cycle potential: A.x -> B, B.a -> A. */
function makeSchemas(): SchemaRegistry {
  return new SchemaRegistry().register({
    version: "v1",
    types: {
      A: {
        fields: {
          x: { kind: "ref", target: "B" },
          xs: { kind: "ref[]", target: "B" },
          n: { kind: "number" },
        },
      },
      B: {
        fields: {
          a: { kind: "ref", target: "A" },
          label: { kind: "string", required: true },
        },
        incoming: { max: 4 },
      },
    },
  });
}

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function snapshot(repo: Repository): string {
  return JSON.stringify(
    repo.all().map((d) => [d.id, d.type, d.schemaVersion, d.data]),
  );
}

function assertIndexMatchesValidator(repo: Repository, schemas: SchemaRegistry): void {
  const full = validateAll(repo.all(), schemas);
  const ids = new Set<string>([
    ...repo.all().map((d) => d.id),
    ...full.incoming.keys(),
  ]);
  for (const id of ids) {
    const fromRepo: Record<string, string[]> = {};
    for (const [src, paths] of repo.incomingOf(id)) {
      fromRepo[src] = [...paths].sort();
    }
    const fromValidator: Record<string, string[]> = {};
    for (const [src, paths] of full.incoming.get(id) ?? []) {
      fromValidator[src] = [...paths].sort();
    }
    assert.deepEqual(fromRepo, fromValidator, `reverse index mismatch for target "${id}"`);
  }
}

test("independent full validator cross-checks random batches", () => {
  const schemas = makeSchemas();
  const repo = new Repository(schemas);
  const rand = mulberry32(0xc0ffee);

  let seq = 0;
  let okCommits = 0;
  let failedCommits = 0;
  let renames = 0;
  let deletes = 0;

  const pickId = (): string | undefined => {
    const ids = repo.all().map((d) => d.id);
    if (ids.length === 0) return undefined;
    return ids[Math.floor(rand() * ids.length)];
  };

  for (let round = 0; round < 300; round++) {
    const before = snapshot(repo);
    const tx = repo.transaction();
    const batchNewIds: string[] = [];
    const opCount = 1 + Math.floor(rand() * 5);

    for (let i = 0; i < opCount; i++) {
      const roll = rand();
      if (roll < 0.5) {
        // put a fresh document, referencing existing or same-batch ids (cycles allowed)
        const id = `d${seq++}`;
        batchNewIds.push(id);
        const isA = rand() < 0.5;
        const candidates = [...repo.all().map((d) => d.id), ...batchNewIds];
        const pickRef = (type: "A" | "B"): string | undefined => {
          const pool = candidates.filter((c) => {
            const doc = repo.get(c);
            if (doc) return doc.type === type;
            return false; // same-batch ids: type known only for current id
          });
          if (type === "B" && isA) pool.push(...batchNewIds.filter((x) => x !== id || rand() < 0.3));
          if (pool.length === 0) return undefined;
          return pool[Math.floor(rand() * pool.length)];
        };
        if (isA) {
          const data: Record<string, unknown> = { n: Math.floor(rand() * 10) };
          const x = pickRef("B");
          if (x && rand() < 0.8) data.x = x;
          if (rand() < 0.4) {
            const xs = [pickRef("B"), pickRef("B")].filter((v): v is string => !!v);
            data.xs = xs;
          }
          tx.put({ id, type: "A", schemaVersion: "v1", data });
        } else {
          const data: Record<string, unknown> = { label: id };
          const a = pickRef("A");
          if (a && rand() < 0.7) data.a = a;
          tx.put({ id, type: "B", schemaVersion: "v1", data });
        }
      } else if (roll < 0.62) {
        const id = pickId();
        if (id) {
          renames++;
          tx.rename(id, `r${seq++}`);
        }
      } else if (roll < 0.74) {
        const id = pickId();
        if (id) {
          deletes++;
          tx.delete(id);
        }
      } else if (roll < 0.82) {
        // intentionally invalid: dangling reference
        const id = `d${seq++}`;
        tx.put({ id, type: "A", schemaVersion: "v1", data: { x: `ghost-${seq}` } });
      } else if (roll < 0.9) {
        // intentionally invalid: duplicate id, different content
        const id = `d${seq++}`;
        tx.put({ id, type: "B", schemaVersion: "v1", data: { label: "one" } });
        tx.put({ id, type: "B", schemaVersion: "v1", data: { label: "two" } });
      } else {
        // replace an existing document (upsert) with fresh random refs
        const id = pickId();
        if (id) {
          const doc = repo.get(id)!;
          if (doc.type === "A") {
            tx.put({ ...doc, data: { n: Math.floor(rand() * 100) } });
          } else {
            tx.put({ ...doc, data: { label: `re-${seq}` } });
          }
        }
      }
    }

    const result = tx.commit();
    const full = validateAll(repo.all(), schemas);

    if (result.ok) {
      okCommits++;
      assert.equal(
        full.errors.length,
        0,
        `validator disagrees with accepted commit: ${JSON.stringify(full.errors, null, 2)}`,
      );
    } else {
      failedCommits++;
      assert.equal(snapshot(repo), before, "failed commit must leave state untouched");
      assert.equal(full.errors.length, 0, "state after rejected commit must stay valid");
    }
    assertIndexMatchesValidator(repo, schemas);
  }

  assert.ok(okCommits > 100, `expected many accepted commits, got ${okCommits}`);
  assert.ok(failedCommits > 10, `expected some rejected commits, got ${failedCommits}`);
  assert.ok(renames > 10, "rename path must be exercised");
  assert.ok(deletes > 10, "delete path must be exercised");
  console.log(
    `cross-check: 300 random batches -> ${okCommits} accepted, ${failedCommits} rejected, ` +
      `${renames} renames, ${deletes} deletes; repo+validator agree everywhere`,
  );
});

test("reference cycles are legal and leave no residual errors after entry-point deletion", () => {
  const schemas = makeSchemas();
  const repo = new Repository(schemas);

  // a1 -> b1 -> a1 : a pure cycle, plus an entry point a0 -> b1
  const res = repo.batch((tx) => {
    tx.put({ id: "a1", type: "A", schemaVersion: "v1", data: { x: "b1" } });
    tx.put({ id: "b1", type: "B", schemaVersion: "v1", data: { label: "b1", a: "a1" } });
    tx.put({ id: "a0", type: "A", schemaVersion: "v1", data: { x: "b1" } });
  });
  assert.equal(res.ok, true);
  assert.equal(repo.incomingCount("a1"), 1);
  assert.equal(repo.incomingCount("b1"), 2);

  // deleting only the entry point keeps the cycle valid
  assert.equal(repo.batch((tx) => tx.delete("a0")).ok, true);
  assert.equal(validateAll(repo.all(), schemas).errors.length, 0);

  // deleting the whole cycle leaves nothing behind: zero residual errors, empty index
  const gone = repo.batch((tx) => {
    tx.delete("a1");
    tx.delete("b1");
  });
  assert.equal(gone.ok, true);
  assert.equal(repo.size, 0);
  const full = validateAll(repo.all(), schemas);
  assert.equal(full.errors.length, 0);
  assert.equal(full.incoming.size, 0);
  assert.equal(repo.incomingCount("a1"), 0);
  assert.equal(repo.incomingCount("b1"), 0);
});
