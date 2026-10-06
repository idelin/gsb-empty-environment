import assert from "node:assert/strict";
import test from "node:test";
import { SuggestionMerger } from "../src/merger.ts";

test("dedupes candidates by canonical key across sources", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("lsp", [{ key: "React", score: 3 }]);
  merger.replaceSource("history", [
    { key: "  react  ", score: 7 },
    { key: "REACT", score: 5 },
  ]);
  const list = merger.list();
  assert.equal(list.length, 1);
  assert.equal(list[0].id, "react");
  assert.deepEqual(list[0].sources, ["history", "lsp"]);
});

test("keeps max score and full source provenance set", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("a", [{ key: "npm", score: 4, detail: "from a" }]);
  merger.replaceSource("b", [{ key: "npm", score: 9, detail: "from b" }]);
  merger.replaceSource("c", [{ key: "npm", score: 6 }]);
  const [item] = merger.list();
  assert.equal(item.score, 9);
  assert.equal(item.detail, "from b");
  assert.deepEqual(item.sources, ["a", "b", "c"]);
});

test("re-selects winner after a source withdraws", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("strong", [{ key: "git", score: 10, detail: "strong" }]);
  merger.replaceSource("weak", [{ key: "git", score: 4, detail: "weak" }]);
  assert.equal(merger.list()[0].score, 10);

  const list = merger.withdrawSource("strong");
  assert.equal(list.length, 1);
  assert.equal(list[0].score, 4);
  assert.equal(list[0].detail, "weak");
  assert.deepEqual(list[0].sources, ["weak"]);

  assert.equal(merger.withdrawSource("weak").length, 0);
});

test("replaceSource supersedes the source's previous candidates", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("s", [{ key: "old", score: 1 }]);
  merger.replaceSource("s", [{ key: "new", score: 2 }]);
  assert.deepEqual(merger.list().map((s) => s.id), ["new"]);
});

test("sorts by score desc with deterministic tie-breaks", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("s", [
    { key: "beta", score: 5 },
    { key: "alpha", score: 5 },
    { key: "gamma", score: 8 },
  ]);
  assert.deepEqual(merger.list().map((s) => s.id), ["gamma", "alpha", "beta"]);
});

test("highlight survives reordering by identity", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("a", [
    { key: "one", score: 1 },
    { key: "two", score: 2 },
  ]);
  merger.highlight("one");
  merger.replaceSource("b", [{ key: "three", score: 3 }]);
  assert.deepEqual(merger.list().map((s) => s.id), ["three", "two", "one"]);
  assert.equal(merger.highlighted()?.id, "one");
});

test("highlight falls back to clamped index when its item vanishes", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("a", [
    { key: "x", score: 3 },
    { key: "y", score: 2 },
    { key: "z", score: 1 },
  ]);
  merger.highlight("y");
  merger.withdrawSource("a");
  assert.equal(merger.highlighted(), null);

  merger.replaceSource("b", [
    { key: "p", score: 9 },
    { key: "q", score: 8 },
    { key: "r", score: 7 },
  ]);
  merger.highlight("r");
  merger.replaceSource("b", [
    { key: "p", score: 9 },
    { key: "q", score: 8 },
  ]);
  assert.equal(merger.highlighted()?.id, "q");
});

test("moveHighlight clamps at list boundaries", () => {
  const merger = new SuggestionMerger();
  merger.replaceSource("a", [
    { key: "a", score: 3 },
    { key: "b", score: 2 },
    { key: "c", score: 1 },
  ]);
  assert.equal(merger.moveHighlight(1)?.id, "b");
  assert.equal(merger.moveHighlight(1)?.id, "c");
  assert.equal(merger.moveHighlight(5)?.id, "c");
  assert.equal(merger.moveHighlight(-99)?.id, "a");
});
