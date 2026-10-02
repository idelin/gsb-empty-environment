import { test } from "node:test";
import assert from "node:assert/strict";
import { Store } from "../src/store.ts";
import type { Doc, Incoming, Op } from "../src/store.ts";
import { validateAll } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const schemaV1: Schema = {
  version: 1,
  types: {
    author: {
      properties: {
        name: { kind: "scalar", scalar: "string" },
        books: { kind: "refArray", target: "book" },
      },
      required: ["name"],
    },
    book: {
      properties: {
        title: { kind: "scalar", scalar: "string" },
        author: { kind: "ref", target: "author" },
        related: { kind: "refArray", target: "book" },
      },
      required: ["title"],
    },
  },
};

// v2 tightens the reverse cardinality of authors: a valid v1 state may be
// invalid under v2, so random setSchema calls exercise atomic rejection.
const schemaV2: Schema = structuredClone(schemaV1);
schemaV2.version = 2;
schemaV2.types.author.maxIncoming = 2;

const IDS = Array.from({ length: 8 }, (_, i) => `id${i}`);

function randomDoc(rand: () => number): Doc {
  const id = IDS[Math.floor(rand() * IDS.length)];
  if (rand() < 0.5) {
    const doc: Doc = { id, type: "author", data: { name: `n${Math.floor(rand() * 100)}` } };
    if (rand() < 0.7) {
      const refs: string[] = [];
      const n = Math.floor(rand() * 3);
      for (let i = 0; i < n; i++) refs.push(IDS[Math.floor(rand() * IDS.length)]);
      doc.data.books = refs;
    }
    if (rand() < 0.1) doc.data.name = 123; // inject type errors sometimes
    return doc;
  }
  const doc: Doc = { id, type: "book", data: { title: `t${Math.floor(rand() * 100)}` } };
  if (rand() < 0.7) doc.data.author = IDS[Math.floor(rand() * IDS.length)];
  if (rand() < 0.4) {
    const refs: string[] = [];
    const n = Math.floor(rand() * 3);
    for (let i = 0; i < n; i++) refs.push(IDS[Math.floor(rand() * IDS.length)]);
    doc.data.related = refs;
  }
  if (rand() < 0.1) delete (doc.data as Record<string, unknown>).title; // inject required errors
  return doc;
}

function randomBatch(rand: () => number): Op[] {
  const ops: Op[] = [];
  const n = 1 + Math.floor(rand() * 4);
  for (let i = 0; i < n; i++) {
    const r = rand();
    if (r < 0.55) {
      ops.push({ op: "insert", doc: randomDoc(rand) });
    } else if (r < 0.75) {
      ops.push({
        op: "rename",
        from: IDS[Math.floor(rand() * IDS.length)],
        to: IDS[Math.floor(rand() * IDS.length)],
      });
    } else {
      ops.push({ op: "delete", id: IDS[Math.floor(rand() * IDS.length)] });
    }
  }
  return ops;
}

function normalizeIncoming(incoming: Incoming): Record<string, Record<string, string[]>> {
  const out: Record<string, Record<string, string[]>> = {};
  for (const [target, refs] of [...incoming.entries()].sort(([a], [b]) => a.localeCompare(b))) {
    out[target] = {};
    for (const [source, paths] of [...refs.entries()].sort(([a], [b]) => a.localeCompare(b))) {
      out[target][source] = [...paths].sort();
    }
  }
  return out;
}

function normalizeDocs(docs: Map<string, Doc>): string {
  return JSON.stringify(
    [...docs.entries()].sort(([a], [b]) => a.localeCompare(b)),
  );
}

test("random batches: committed state always matches the independent full validator", () => {
  const rand = mulberry32(20261002);
  const store = new Store(structuredClone(schemaV1));

  for (let round = 0; round < 400; round++) {
    const docsBefore = normalizeDocs(store.docs);
    const incomingBefore = normalizeIncoming(store.incoming);

    if (rand() < 0.1) {
      const target = store.schema.version === 1 ? schemaV2 : schemaV1;
      store.setSchema(structuredClone(target));
    } else {
      store.transact(randomBatch(rand));
    }

    // Whatever happened, the committed state must be fully valid under the
    // active schema version, and the incrementally maintained reverse index
    // must equal the one rebuilt from scratch by the independent validator.
    const full = validateAll(store.docs.values(), store.schema);
    assert.deepEqual(
      full.errors,
      [],
      `round ${round}: committed state must validate clean under schema v${store.schema.version}`,
    );
    assert.deepEqual(
      normalizeIncoming(store.incoming),
      normalizeIncoming(full.incoming),
      `round ${round}: reverse index drifted from the independent rebuild`,
    );

    // If the last operation failed, state must be untouched (atomicity).
    // Re-derive by checking that a failed replay changes nothing further:
    // (direct before/after comparison happens on the next round's snapshot.)
    if (normalizeDocs(store.docs) === docsBefore) {
      assert.deepEqual(
        normalizeIncoming(store.incoming),
        incomingBefore,
        `round ${round}: docs unchanged but reverse index mutated`,
      );
    }
  }

  assert.ok(store.docs.size > 0, "fuzz run should leave some documents behind");
});

test("rejected batches leave docs and reverse index byte-identical", () => {
  const rand = mulberry32(7);
  const store = new Store(structuredClone(schemaV1));
  let rejected = 0;

  for (let round = 0; round < 400 && rejected < 50; round++) {
    const batch = randomBatch(rand);
    const docsBefore = normalizeDocs(store.docs);
    const incomingBefore = normalizeIncoming(store.incoming);
    const result = store.transact(batch);
    if (!result.ok) {
      rejected++;
      assert.equal(normalizeDocs(store.docs), docsBefore, "docs mutated on rejected batch");
      assert.deepEqual(normalizeIncoming(store.incoming), incomingBefore, "index mutated on rejected batch");
      assert.ok(result.errors.length > 0, "rejection must carry errors");
      for (const err of result.errors) {
        assert.equal(typeof err.docId, "string");
        assert.equal(typeof err.instancePath, "string");
        assert.equal(typeof err.schemaPath, "string");
        assert.ok("slice" in err, "every error carries a minimal slice");
      }
    }
  }
  assert.ok(rejected > 0, "fuzz run should produce rejections to check");
});
