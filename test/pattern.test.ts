import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { expandPattern, parsePattern, PatternError } from "../src/index.ts";

describe("parsePattern", () => {
  it("parses literal and named segments", () => {
    const p = parsePattern("/users/:id/posts");
    assert.deepEqual(
      p.nodes.map((n) => n.kind),
      ["literal", "param", "literal"],
    );
    assert.deepEqual(p.paramNames, ["id"]);
  });

  it("parses root and ignores duplicate/trailing slashes", () => {
    assert.deepEqual(parsePattern("/").nodes, []);
    assert.deepEqual(parsePattern("/a//b/").nodes.length, 2);
  });

  it("parses trailing splat", () => {
    const p = parsePattern("/files/*rest");
    assert.equal(p.nodes[1].kind, "splat");
    assert.deepEqual(p.paramNames, ["rest"]);
  });

  it("parses nested optional groups", () => {
    const p = parsePattern("/a(/b(/:c))");
    assert.equal(p.nodes.length, 2);
    const g = p.nodes[1];
    assert.equal(g.kind, "group");
    assert.deepEqual(p.paramNames, ["c"]);
  });

  it("rejects splat not at the end", () => {
    assert.throws(() => parsePattern("/*rest/x"), PatternError);
  });

  it("rejects splat inside an optional group", () => {
    assert.throws(() => parsePattern("/a(/*rest)"), PatternError);
  });

  it("rejects empty group, unmatched parens, bad names, duplicates", () => {
    assert.throws(() => parsePattern("/a()"), PatternError);
    assert.throws(() => parsePattern("/a(/b"), PatternError);
    assert.throws(() => parsePattern("/a)"), PatternError);
    assert.throws(() => parsePattern("/a/:"), PatternError);
    assert.throws(() => parsePattern("/:id/:id"), PatternError);
    assert.throws(() => parsePattern("a/b"), PatternError);
  });
});

describe("expandPattern", () => {
  it("expands optional groups with omission counts", () => {
    const p = parsePattern("/a(/b)(/:c)");
    const exps = expandPattern(p.nodes);
    assert.equal(exps.length, 4);
    const key = (e: (typeof exps)[number]) =>
      `${e.segments.map((s) => (s.kind === "literal" ? s.value : s.kind)).join("/")}|${e.literals}|${e.omissions}`;
    assert.deepEqual(
      exps.map(key).sort(),
      ["a|1|2", "a/b|2|1", "a/param|1|1", "a/b/param|2|0"].sort(),
    );
  });

  it("caps combinatorial explosion", () => {
    const p = parsePattern("/a(/b)(/c)(/d)(/e)(/f)(/g)(/h)(/i)");
    assert.throws(() => expandPattern(p.nodes, 16), /more than 16/);
  });
});
