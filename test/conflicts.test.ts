import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { compileRouter, ConflictError } from "../src/index.ts";

function compile(patterns: string[], onConflict?: "collect" | "throw") {
  return compileRouter(
    patterns.map((pattern, i) => ({ pattern, handler: `h${i}` })),
    { onConflict },
  );
}

describe("compile-time same-priority overlap reports", () => {
  it("reports param-vs-param overlap with a witness", () => {
    const r = compile(["/:a/x", "/:b/x"]);
    assert.equal(r.conflicts.length, 1);
    const c = r.conflicts[0];
    assert.equal(c.firstIndex, 0);
    assert.equal(c.secondIndex, 1);
    assert.match(c.witness, /^\/<v\d+>\/x$/);
    assert.deepEqual(c.score, { literals: 1, omissions: 0 });
  });

  it("does not report when literals break the tie", () => {
    const r = compile(["/users/:id", "/users/new"]);
    assert.equal(r.conflicts.length, 0);
  });

  it("does not report when omissions break the tie", () => {
    const r = compile(["/a(/b)", "/a"]);
    assert.equal(r.conflicts.length, 0);
  });

  it("reports optional-group vs concrete overlap", () => {
    const r = compile(["/a(/b)", "/a/b"]);
    assert.equal(r.conflicts.length, 1);
    assert.equal(r.conflicts[0].witness, "/a/b");
    assert.deepEqual(r.conflicts[0].score, { literals: 2, omissions: 0 });
  });

  it("reports splat vs param overlap", () => {
    const r = compile(["/f/*rest", "/f/:id"]);
    assert.equal(r.conflicts.length, 1);
    assert.match(r.conflicts[0].witness, /^\/f\/<v\d+>$/);
  });

  it("reports duplicate registrations", () => {
    const r = compile(["/a/:id", "/a/:id"]);
    assert.equal(r.conflicts.length, 1);
    assert.equal(r.conflicts[0].witness, "/a/<v0>");
  });

  it("reports nested-group ambiguities", () => {
    // /x(/y)(/z) 展开含 [x,y](省1) 与 /x(/z)/y 展开含 [x,y](省1)：同字面同省略
    const r = compile(["/x(/y)(/z)", "/x(/z)/y"]);
    assert.equal(r.conflicts.length, 1);
    assert.equal(r.conflicts[0].witness, "/x/y");
  });

  it("keeps clean tables conflict-free", () => {
    const r = compile([
      "/",
      "/a",
      "/a/:b",
      "/a/b/c",
      "/files/*path",
      "/users/:id/posts(/:postId)",
    ]);
    assert.deepEqual(r.conflicts, []);
  });

  it("throws on conflict when requested", () => {
    assert.throws(() => compile(["/:a", "/:b"], "throw"), ConflictError);
  });
});
