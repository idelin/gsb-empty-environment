/**
 * Randomized cross-check of the offset-based main implementation
 * against the independent line-by-line reference implementation,
 * plus byte-exact undo fuzzing.
 */
import test from "node:test";
import assert from "node:assert/strict";
import { Editor } from "../src/editor.ts";
import { applyEdits, invertEdits, mapSelections } from "../src/edits.ts";
import type { Selection } from "../src/edits.ts";
import { computeIndentEdits, computeUnindentEdits } from "../src/indent.ts";
import { referenceApply } from "../src/reference.ts";

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

function randomText(rand: () => number): string {
  const lineCount = 1 + Math.floor(rand() * 6);
  const lines: string[] = [];
  for (let i = 0; i < lineCount; i++) {
    let line = "";
    const wsLen = Math.floor(rand() * 7);
    for (let j = 0; j < wsLen; j++) line += rand() < 0.4 ? "\t" : " ";
    const bodyLen = Math.floor(rand() * 8);
    for (let j = 0; j < bodyLen; j++) {
      const r = rand();
      line += r < 0.1 ? "\t" : r < 0.2 ? " " : r < 0.25 ? "中" : String.fromCharCode(97 + Math.floor(rand() * 26));
    }
    lines.push(line);
  }
  let text = lines.join("\n");
  if (rand() < 0.5) text += "\n";
  return text;
}

function randomSelections(rand: () => number, len: number): Selection[] {
  const count = 1 + Math.floor(rand() * 5);
  const sels: Selection[] = [];
  for (let i = 0; i < count; i++) {
    sels.push({
      anchor: Math.floor(rand() * (len + 1)),
      head: Math.floor(rand() * (len + 1)),
    });
  }
  return sels;
}

test("main implementation matches line-by-line reference (indent + unindent)", () => {
  const rand = mulberry32(0x5eed);
  for (let iter = 0; iter < 2000; iter++) {
    const text = randomText(rand);
    const sels = randomSelections(rand, text.length);
    const mode = rand() < 0.5 ? "indent" : "unindent";
    const useTabs = rand() < 0.3;
    const opts = { useTabs };

    const edits =
      mode === "indent" ? computeIndentEdits(text, sels, opts) : computeUnindentEdits(text, sels, opts);
    const mainText = applyEdits(text, edits);
    const mainSels = mapSelections(sels, edits);

    const ref = referenceApply(text, sels, mode, opts);

    assert.equal(mainText, ref.text, `text mismatch (iter ${iter}, ${mode})`);
    assert.deepEqual(mainSels, ref.selections, `selection mismatch (iter ${iter}, ${mode})`);
  }
});

test("undo restores the original bytes after any operation sequence", () => {
  const rand = mulberry32(0x0d0);
  for (let iter = 0; iter < 500; iter++) {
    const text = randomText(rand);
    const sels = randomSelections(rand, text.length);
    const originalBytes = Buffer.from(text, "utf8");
    const ed = new Editor(text, sels);
    const ops = 1 + Math.floor(rand() * 8);
    for (let k = 0; k < ops; k++) {
      if (rand() < 0.5) ed.indent({ useTabs: rand() < 0.3 });
      else ed.unindent();
    }
    while (ed.canUndo) ed.undo();
    assert.ok(
      Buffer.from(ed.text, "utf8").equals(originalBytes),
      `byte mismatch after undo (iter ${iter})`,
    );
    assert.deepEqual(ed.selections, sels, `selection mismatch after undo (iter ${iter})`);
  }
});

test("invertEdits round-trips every generated edit set", () => {
  const rand = mulberry32(42);
  for (let iter = 0; iter < 1000; iter++) {
    const text = randomText(rand);
    const sels = randomSelections(rand, text.length);
    const edits =
      rand() < 0.5 ? computeIndentEdits(text, sels) : computeUnindentEdits(text, sels);
    const restored = applyEdits(applyEdits(text, edits), invertEdits(text, edits));
    assert.equal(restored, text);
  }
});
