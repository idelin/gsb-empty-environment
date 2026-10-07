import { test } from "node:test";
import assert from "node:assert/strict";
import { expand, parsePattern, PatternError, type Segment } from "../src/pattern.ts";

function segTexts(pattern: string): Segment[][] {
  return expand(parsePattern(pattern)).map((v) => v.segments);
}

test("parses literal, param and wildcard segments", () => {
  const [variant] = segTexts("/users/:id/*rest");
  assert.deepEqual(variant, [
    { kind: "lit", text: "users" },
    { kind: "param", name: "id" },
    { kind: "wild", name: "rest" },
  ]);
});

test("root pattern expands to a single empty variant", () => {
  const variants = expand(parsePattern("/"));
  assert.equal(variants.length, 1);
  assert.deepEqual(variants[0]!.segments, []);
});

test("optional groups expand to include/omit variants", () => {
  const variants = expand(parsePattern("/a(/b)(/c/:d)"));
  const shapes = variants
    .map((v) => ({
      segs: v.segments.map((s) => (s.kind === "lit" ? s.text : s.kind === "param" ? `:${s.name}` : `*${s.name}`)),
      omitted: v.omittedCount,
      literals: v.literalCount,
    }))
    .sort((x, y) => x.omitted - y.omitted || x.segs.join("/").localeCompare(y.segs.join("/")));
  assert.deepEqual(shapes, [
    { segs: ["a", "b", "c", ":d"], omitted: 0, literals: 3 },
    { segs: ["a", "b"], omitted: 1, literals: 2 },
    { segs: ["a", "c", ":d"], omitted: 1, literals: 2 },
    { segs: ["a"], omitted: 2, literals: 1 },
  ]);
});

test("nested optional groups expand recursively", () => {
  const variants = expand(parsePattern("/base(/a(/b))"));
  const shapes = variants
    .map((v) => ({ text: v.segments.map((s) => (s as { text?: string }).text ?? "").join("/"), omitted: v.omittedCount }))
    .sort((x, y) => x.text.localeCompare(y.text));
  assert.deepEqual(shapes, [
    { text: "base", omitted: 1 },
    { text: "base/a", omitted: 1 },
    { text: "base/a/b", omitted: 0 },
  ]);
});

test("rejects malformed patterns", () => {
  const bad = [
    "no-leading-slash",
    "/a//b",
    "/a/",
    "/:",
    "/*",
    "/:1bad",
    "/*rest/more",
    "/a(/b",
    "/a(/b))",
    "/a()",
    "/a(b)",
    "/a(/b/*rest)",
    "/:id/other/:id",
    "/*rest/:rest",
  ];
  for (const source of bad) {
    assert.throws(() => parsePattern(source), PatternError, source);
  }
});

test("literal segments may contain percent signs and colons", () => {
  const [variant] = segTexts("/a%20b/c:d");
  assert.deepEqual(variant, [
    { kind: "lit", text: "a%20b" },
    { kind: "lit", text: "c:d" },
  ]);
});
