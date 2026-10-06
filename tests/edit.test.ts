import { test, assert, mulberry32 } from "./harness.ts";
import { reprint } from "../src/parser.ts";
import { semantics } from "../src/semantics.ts";
import type { SemNode } from "../src/semantics.ts";
import {
  parseDocument,
  setValue,
  removeBlock,
  addBlock,
} from "../src/edit.ts";
import type { Document, EditResult } from "../src/edit.ts";

const BASE = [
  "# server config",
  "server:",
  "    host: example.com",
  "    port: 8080",
  "    tls:",
  "        enabled: false",
  "        cert: /tmp/cert.pem",
  "",
  "database:",
  "    url: postgres://localhost/db",
  "# end",
  "",
].join("\n");

function assertOk(r: EditResult): Extract<EditResult, { ok: true }> {
  assert.ok(r.ok, `edit rejected: ${JSON.stringify(!r.ok && r.diagnostics)}`);
  return r as Extract<EditResult, { ok: true }>;
}

/** Byte-preservation invariant: everything outside the changed range is identical. */
function assertOnlyRegionChanged(before: string, r: Extract<EditResult, { ok: true }>): void {
  const after = r.doc.src;
  assert.equal(after.slice(0, r.range.start), before.slice(0, r.originalRange.start));
  assert.equal(after.slice(r.range.end), before.slice(r.originalRange.end));
}

/** Round-trip invariant: reprint is identity and reparsing is clean and stable. */
function assertRoundTrip(doc: Document): void {
  assert.equal(reprint(doc.file), doc.src);
  const again = parseDocument(doc.src);
  assert.deepEqual(again.diagnostics, []);
  assert.deepEqual(semantics(again.file), semantics(doc.file));
}

test("setValue reprints only the touched line", () => {
  const doc = parseDocument(BASE);
  const r = assertOk(setValue(doc, ["server", "port"], "9090"));
  assert.equal(r.doc.src, BASE.replace("    port: 8080", "    port: 9090"));
  // The changed region is exactly the modified physical line.
  assert.equal(BASE.slice(r.originalRange.start, r.originalRange.end), "    port: 8080\n");
  assert.equal(r.doc.src.slice(r.range.start, r.range.end), "    port: 9090\n");
  assertOnlyRegionChanged(BASE, r);
  assertRoundTrip(r.doc);
  const sem = semantics(r.doc.file);
  assert.equal(sem[0]!.children[1]!.value, "9090");
});

test("setValue preserves separator style and CRLF", () => {
  const doc = parseDocument("a:   1\r\nb: 2\r\n");
  const r = assertOk(setValue(doc, ["a"], "x"));
  assert.equal(r.doc.src, "a:   x\r\nb: 2\r\n");
  assertRoundTrip(r.doc);
});

test("setValue on a childless opener keeps the entry a leaf", () => {
  const doc = parseDocument("a:\nempty:\nb: 2\n");
  const r = assertOk(setValue(doc, ["empty"], "v"));
  assert.equal(r.doc.src, "a:\nempty: v\nb: 2\n");
  assertRoundTrip(r.doc);
});

test("setValue that would orphan children is atomically rejected", () => {
  const doc = parseDocument(BASE);
  const r = setValue(doc, ["server"], "oops");
  assert.ok(!r.ok);
  assert.equal(r.diagnostics[0]!.code, "unexpected-indent");
  // Diagnostic is located in the ORIGINAL document (first child line).
  assert.equal(r.diagnostics[0]!.line, 3);
  // The original document is untouched.
  assert.equal(doc.src, BASE);
});

test("removeBlock deletes a nested block and nothing else", () => {
  const doc = parseDocument(BASE);
  const r = assertOk(removeBlock(doc, ["server", "tls"]));
  const expected = [
    "# server config",
    "server:",
    "    host: example.com",
    "    port: 8080",
    "",
    "database:",
    "    url: postgres://localhost/db",
    "# end",
    "",
  ].join("\n");
  assert.equal(r.doc.src, expected);
  assert.equal(
    BASE.slice(r.originalRange.start, r.originalRange.end),
    "    tls:\n        enabled: false\n        cert: /tmp/cert.pem\n",
  );
  assertOnlyRegionChanged(BASE, r);
  assertRoundTrip(r.doc);
  const sem = semantics(r.doc.file);
  assert.deepEqual(
    sem[0]!.children.map((n) => n.key),
    ["host", "port"],
  );
});

test("removeBlock of a top-level block keeps trailing comments", () => {
  const doc = parseDocument(BASE);
  const r = assertOk(removeBlock(doc, ["database"]));
  assert.equal(
    r.doc.src,
    BASE.replace("database:\n    url: postgres://localhost/db\n", ""),
  );
  assertOnlyRegionChanged(BASE, r);
  assertRoundTrip(r.doc);
});

test("addBlock inserts a nested block under an existing parent", () => {
  const doc = parseDocument(BASE);
  const r = assertOk(addBlock(doc, ["database"], "replica:\n    host: r1\n    port: 5433"));
  const expected = BASE.replace(
    "    url: postgres://localhost/db\n",
    "    url: postgres://localhost/db\n    replica:\n        host: r1\n        port: 5433\n",
  );
  assert.equal(r.doc.src, expected);
  assertOnlyRegionChanged(BASE, r);
  assertRoundTrip(r.doc);
  const db = semantics(r.doc.file)[1]!;
  assert.deepEqual(db.children[1], {
    key: "replica",
    value: null,
    children: [
      { key: "host", value: "r1", children: [] },
      { key: "port", value: "5433", children: [] },
    ],
  });
});

test("addBlock at the top level appends at end of file", () => {
  const doc = parseDocument(BASE);
  const r = assertOk(addBlock(doc, [], "metrics:\n    port: 9999"));
  assert.equal(r.doc.src, BASE + "metrics:\n    port: 9999\n");
  assertOnlyRegionChanged(BASE, r);
  assertRoundTrip(r.doc);
});

test("addBlock terminates a missing final newline", () => {
  const doc = parseDocument("a: 1");
  const r = assertOk(addBlock(doc, [], "b: 2"));
  assert.equal(r.doc.src, "a: 1\nb: 2\n");
  assertRoundTrip(r.doc);
});

test("addBlock with an ambiguous indent prefix is atomically rejected", () => {
  const doc = parseDocument(BASE); // column 8 is used as 8 spaces under tls:
  const r = addBlock(doc, ["database"], "replica:\n\thost: r1");
  assert.ok(!r.ok);
  assert.equal(r.diagnostics[0]!.code, "ambiguous-indent");
  // The inserted line maps back to its anchor in the ORIGINAL document.
  assert.equal(r.diagnostics[0]!.line, 9); // "database:" line
  assert.match(r.diagnostics[0]!.message, /inserted text/);
  assert.equal(doc.src, BASE); // untouched
});

test("remove then re-add of a nested block restores the semantics", () => {
  const doc = parseDocument(BASE);
  const removed = assertOk(removeBlock(doc, ["server", "tls"]));
  const restored = assertOk(
    addBlock(removed.doc, ["server"], "tls:\n    enabled: false\n    cert: /tmp/cert.pem"),
  );
  assert.equal(restored.doc.src, BASE);
  assert.deepEqual(semantics(restored.doc.file), semantics(doc.file));
  assertRoundTrip(restored.doc);
});

// --- Randomized parse -> modify -> reprint -> reparse round trips ---------

interface Model {
  key: string;
  value: string | null;
  children: Model[];
}

function genValidDoc(rand: () => number): { src: string; model: Model[]; unit: string } {
  const unit = rand() < 0.5 ? "    " : "\t";
  const lines: string[] = [];
  let counter = 0;
  const emit = (depth: number, count: number): Model[] => {
    const nodes: Model[] = [];
    for (let i = 0; i < count; i++) {
      if (rand() < 0.2) lines.push("");
      if (rand() < 0.2) lines.push(unit.repeat(depth) + "# note");
      const key = `k${counter++}`;
      if (depth < 3 && rand() < 0.45) {
        lines.push(unit.repeat(depth) + key + ":");
        nodes.push({ key, value: null, children: emit(depth + 1, 1 + Math.floor(rand() * 3)) });
      } else {
        const value = `v${Math.floor(rand() * 100)}`;
        lines.push(unit.repeat(depth) + key + ": " + value);
        nodes.push({ key, value, children: [] });
      }
    }
    return nodes;
  };
  const model = emit(0, 1 + Math.floor(rand() * 4));
  return { src: lines.join("\n") + "\n", model, unit };
}

function modelPaths(model: Model[], prefix: string[] = []): string[][] {
  const out: string[][] = [];
  for (const n of model) {
    const p = [...prefix, n.key];
    out.push(p);
    out.push(...modelPaths(n.children, p));
  }
  return out;
}

function modelFind(model: Model[], path: string[]): Model {
  let level = model;
  let node: Model | undefined;
  for (const key of path) {
    node = level.find((n) => n.key === key);
    assert.ok(node, `model path ${path}`);
    level = node.children;
  }
  return node!;
}

function modelRemove(model: Model[], path: string[]): void {
  const parent = path.length === 1 ? model : modelFind(model, path.slice(0, -1)).children;
  const i = parent.findIndex((n) => n.key === path[path.length - 1]);
  parent.splice(i, 1);
}

function stripModel(m: Model): SemNode {
  return { key: m.key, value: m.value, children: m.children.map(stripModel) };
}

test("randomized edit round trips keep semantics and untouched bytes", () => {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = mulberry32(10000 + seed);
    const { src, model, unit } = genValidDoc(rand);
    let doc = parseDocument(src);
    assert.deepEqual(doc.diagnostics, [], `seed ${seed} generated doc must be clean`);
    assert.deepEqual(semantics(doc.file), model.map(stripModel), `seed ${seed} initial semantics`);

    const edits = 1 + Math.floor(rand() * 4);
    for (let e = 0; e < edits; e++) {
      const paths = modelPaths(model);
      if (paths.length === 0) break;
      const leaves = paths.filter((p) => modelFind(model, p).children.length === 0);
      const openers = paths.filter((p) => modelFind(model, p).value === null);
      const kind = rand();
      const before = doc.src;
      let r: EditResult;
      let path: string[];
      if (kind < 0.4 && leaves.length > 0) {
        // setValue only applies to leaves; a block parent would orphan children.
        path = leaves[Math.floor(rand() * leaves.length)]!;
        const value = `n${Math.floor(rand() * 1000)}`;
        r = setValue(doc, path, value);
        modelFind(model, path).value = value;
      } else if (kind < 0.7) {
        path = paths[Math.floor(rand() * paths.length)]!;
        r = removeBlock(doc, path);
        modelRemove(model, path);
      } else if (openers.length > 0) {
        // addBlock only applies below a block opener ('key:' with no value).
        path = openers[Math.floor(rand() * openers.length)]!;
        const key = `a${Math.floor(rand() * 1000)}`;
        // Use the document's own indent unit so the inserted block stays consistent.
        r = addBlock(doc, path, `${key}:\n${unit}leaf: 1`);
        modelFind(model, path).children.push({
          key,
          value: null,
          children: [{ key: "leaf", value: "1", children: [] }],
        });
      } else {
        continue;
      }
      const ok = assertOk(r);
      doc = ok.doc;
      assertOnlyRegionChanged(before, ok);
      assert.equal(reprint(doc.file), doc.src, `seed ${seed} reprint identity`);
      const reparsed = parseDocument(doc.src);
      assert.deepEqual(reparsed.diagnostics, [], `seed ${seed} reparse clean`);
      assert.deepEqual(
        semantics(doc.file),
        model.map(stripModel),
        `seed ${seed} edit ${e} semantics (${JSON.stringify(path)})`,
      );
    }
  }
});
