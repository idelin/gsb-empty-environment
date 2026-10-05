/**
 * Randomized differential test: TextEditor (persistent rope) vs. an
 * independent splice-based array model, including undo/redo, branching
 * (checkout), snapshot release and node reclamation checks.
 */
import { TextEditor, InvalidEncodingError } from "../src/index";
import { Model, assert, assertEq, checkRope, makeRng } from "./helpers";

const ALPHABET = ["a", "b", "c", "é", "中", "文", "😀", "🎉", "𝄞", "ẞ", "ᾯ", "", "xy", "中😀", "ééé"];

const BAD_INPUTS: Uint8Array[] = [
  new Uint8Array([0x80]),
  new Uint8Array([0xc0, 0xaf]),
  new Uint8Array([0xed, 0xa0, 0x80]),
  new Uint8Array([0xf4, 0x90, 0x80, 0x80]),
  new Uint8Array([0xe4, 0xb8]),
  new Uint8Array([0xff, 0xfe]),
];

function randomText(rng: () => number): string {
  const n = Math.floor(rng() * 9);
  let s = "";
  for (let i = 0; i < n; i++) {
    s += ALPHABET[Math.floor(rng() * ALPHABET.length)];
  }
  return s;
}

function verify(editor: TextEditor, model: Model, ctx: string): void {
  assertEq(editor.getText(), model.text(), ctx + ": text");
  assertEq(editor.scalarLength, model.scalarLength(), ctx + ": scalarLength");
  assertEq(editor.byteLength, model.byteLength(), ctx + ": byteLength");
  assertEq(editor.currentSnapshotId, model.current, ctx + ": current id");
  assertEq(editor.snapshotCount, model.ids().length, ctx + ": snapshot count");
  assertEq(editor.liveNodes, editor.debugReachableNodes(), ctx + ": node reclamation invariant");
  const agg = checkRope(editor.debugRoot());
  assertEq(agg.scalars, model.scalarLength(), ctx + ": rope scalars");
  assertEq(agg.bytes, model.byteLength(), ctx + ": rope bytes");
}

function runFuzz(seed: number, iterations: number): void {
  const rng = makeRng(seed);
  const editor = new TextEditor();
  const model = new Model();
  let snapshotsTaken = 0;
  let releases = 0;
  let rejected = 0;

  for (let iter = 0; iter < iterations; iter++) {
    const ctx = "seed=" + seed + " iter=" + iter;
    const op = rng();
    if (op < 0.32) {
      // insert
      const offset = Math.floor(rng() * (model.scalarLength() + 1));
      const text = randomText(rng);
      editor.insert(offset, text);
      model.insert(offset, text);
    } else if (op < 0.55) {
      // delete
      const len = model.scalarLength();
      if (len > 0) {
        const offset = Math.floor(rng() * len);
        const count = Math.floor(rng() * (len - offset + 1));
        editor.delete(offset, count);
        model.delete(offset, count);
      }
    } else if (op < 0.65) {
      // undo
      const a = editor.undo();
      const b = model.undo();
      assertEq(a, b, ctx + ": undo result");
    } else if (op < 0.7) {
      // redo
      const a = editor.redo();
      const b = model.redo();
      assertEq(a, b, ctx + ": redo result");
    } else if (op < 0.76) {
      // snapshot
      const id = editor.snapshot("snap-" + snapshotsTaken);
      assertEq(id, model.snapshot(), ctx + ": snapshot id");
      snapshotsTaken++;
    } else if (op < 0.86) {
      // checkout a random live snapshot (branch exploration)
      const ids = model.ids();
      const id = ids[Math.floor(rng() * ids.length)];
      editor.checkout(id);
      model.checkout(id);
    } else if (op < 0.94) {
      // release a random releasable snapshot
      const candidates = model.ids().filter((id) => id !== model.current && model.isLeaf(id));
      if (candidates.length > 0) {
        const id = candidates[Math.floor(rng() * candidates.length)];
        editor.releaseSnapshot(id);
        model.release(id);
        releases++;
      }
    } else {
      // invalid input must be rejected without touching state
      const bad = BAD_INPUTS[Math.floor(rng() * BAD_INPUTS.length)];
      const offset = Math.floor(rng() * (model.scalarLength() + 1));
      let threw = false;
      try {
        editor.insert(offset, bad);
      } catch (e) {
        threw = e instanceof InvalidEncodingError;
      }
      assert(threw, ctx + ": invalid input rejected");
      rejected++;
    }
    verify(editor, model, ctx);
  }
  // sanity: the run actually exercised the interesting features
  assert(snapshotsTaken > 0, "took snapshots");
  assert(releases > 0, "released snapshots");
  assert(rejected > 0, "rejected invalid inputs");
}

export const tests: Array<[string, () => void]> = [
  ["fuzz seed 1 (600 ops)", () => runFuzz(1, 600)],
  ["fuzz seed 2 (600 ops)", () => runFuzz(2, 600)],
  ["fuzz seed 42 (800 ops)", () => runFuzz(42, 800)],
  ["fuzz seed 777 (800 ops)", () => runFuzz(777, 800)],
  ["fuzz seed 1000 (1000 ops)", () => runFuzz(1000, 1000)],
  ["fuzz seed 2024 (1000 ops)", () => runFuzz(2024, 1000)],
];
