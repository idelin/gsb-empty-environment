import test from "node:test";
import assert from "node:assert/strict";
import { anchor, makeNode } from "../src/model.ts";
import type { Doc } from "../src/model.ts";
import { normalize, isNormalized } from "../src/normalize.ts";
import { applyBatch } from "../src/batch.ts";
import type { MarkEdit } from "../src/marks.ts";
import { docLength } from "../src/model.ts";
import { equivalent, flatten, fromFlat } from "../src/flat.ts";

// deterministic PRNG (mulberry32)
function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const MARKS = ["bold", "italic", "underline", "strike"];

function randomDoc(rand: () => number): Doc {
  const nodeCount = 1 + Math.floor(rand() * 6);
  const nodes = [];
  for (let i = 0; i < nodeCount; i++) {
    const len = Math.floor(rand() * 6); // may be empty
    const text = Array.from({ length: len }, () =>
      String.fromCharCode(97 + Math.floor(rand() * 26)),
    ).join("");
    const marks = MARKS.filter(() => rand() < 0.3);
    const anchors = [];
    if (rand() < 0.4) {
      anchors.push(
        anchor(`a${i}`, rand() < 0.5 ? "left" : "right", Math.floor(rand() * (len + 1))),
      );
    }
    nodes.push(makeNode(text, marks, anchors, `n${i}`));
  }
  return { nodes };
}

function randomEdits(rand: () => number, len: number): MarkEdit[] {
  const edits: MarkEdit[] = [];
  const count = 1 + Math.floor(rand() * 4);
  for (let i = 0; i < count; i++) {
    const a = Math.floor(rand() * (len + 1));
    const b = Math.floor(rand() * (len + 1));
    edits.push({
      kind: rand() < 0.5 ? "add" : "remove",
      start: Math.min(a, b),
      end: Math.max(a, b),
      mark: MARKS[Math.floor(rand() * MARKS.length)],
    });
  }
  return edits;
}

test("property: normalize is idempotent and equivalence-preserving", () => {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = rng(seed);
    const doc = randomDoc(rand);
    const once = normalize(doc).doc;
    assert.ok(equivalent(doc, once), `seed ${seed}: equivalence`);
    assert.ok(isNormalized(once), `seed ${seed}: idempotent`);
    assert.deepEqual(normalize(once).doc, once, `seed ${seed}: fixed point`);
  }
});

test("property: batch edits preserve text and anchors, undo restores identity", () => {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = rng(seed);
    const doc = randomDoc(rand);
    const edits = randomEdits(rand, docLength(doc));
    const tx = applyBatch(doc, edits);
    // text untouched by mark edits
    assert.equal(
      flatten(tx.doc).cells.map((c) => c.char).join(""),
      flatten(doc).cells.map((c) => c.char).join(""),
      `seed ${seed}: text preserved`,
    );
    // anchors stay at their boundaries
    assert.deepEqual(
      flatten(tx.doc).boundaries,
      flatten(doc).boundaries,
      `seed ${seed}: anchors preserved`,
    );
    // result is normalized (maximal runs)
    assert.ok(isNormalized(tx.doc), `seed ${seed}: normalized result`);
    // undo restores the exact original structure
    assert.deepEqual(tx.undo(), doc, `seed ${seed}: undo`);
  }
});

test("property: toggling a mark twice over any range is a flat-model no-op", () => {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = rng(seed);
    const doc = randomDoc(rand);
    const len = docLength(doc);
    const [edit] = randomEdits(rand, len);
    // use a mark that never occurs in random docs, so add/remove is a true inverse
    const add: MarkEdit = { ...edit, kind: "add", mark: "toggle-mark" };
    const remove: MarkEdit = { ...edit, kind: "remove", mark: "toggle-mark" };
    const toggled = applyBatch(applyBatch(doc, [add]).doc, [remove]).doc;
    assert.ok(equivalent(doc, toggled), `seed ${seed}: toggle reversibility`);
  }
});

test("flat model round-trip keeps equivalence, anchored empties survive", () => {
  for (let seed = 1; seed <= 200; seed++) {
    const rand = rng(seed);
    const doc = randomDoc(rand);
    const round = fromFlat(flatten(doc));
    assert.ok(equivalent(doc, round), `seed ${seed}: round trip`);
    // a doc with only anchors and no text keeps a single anchored empty node
  }
  const onlyAnchors: Doc = {
    nodes: [makeNode("", [], [anchor("solo", "right", 0)], "solo")],
  };
  const round = fromFlat(flatten(onlyAnchors));
  assert.equal(round.nodes.length, 1);
  assert.equal(round.nodes[0].text, "");
  assert.equal(round.nodes[0].anchors[0].id, "solo");
  assert.ok(equivalent(onlyAnchors, round));
});
