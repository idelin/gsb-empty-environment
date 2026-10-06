import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { compileRouter, createRouter, splitPath } from "../src/index.ts";

function compile(patterns: string[]) {
  return compileRouter(patterns.map((pattern, i) => ({ pattern, handler: `h${i}` })));
}

describe("priority: literals > omissions > registration order", () => {
  it("prefers more literal segments", () => {
    const r = compile(["/users/:id", "/users/new"]);
    assert.equal(r.match("/users/new")?.handler, "h1");
    assert.equal(r.match("/users/other")?.handler, "h0");
  });

  it("prefers fewer optional omissions", () => {
    const r = compile(["/a(/b)/c", "/a/c"]);
    // 两条都是 2 个字面；第一条省略 1 组，第二条省略 0 组
    assert.equal(r.match("/a/c")?.handler, "h1");
    assert.equal(r.match("/a/b/c")?.handler, "h0");
  });

  it("falls back to registration order on a full tie", () => {
    const r = compile(["/a/:x", "/a/:y"]);
    const m = r.match("/a/1");
    assert.equal(m?.handler, "h0");
    assert.deepEqual(m?.params, { x: "1" });
  });

  it("orders matchAll by the same rule", () => {
    const r = compile(["/a/:x", "/a/b", "/a(/b)"]);
    const all = r.matchAll("/a/b");
    assert.deepEqual(
      all.map((m) => m.handler),
      ["h1", "h2", "h0"], // 2 字面 0 省略 > 2 字面 1 省略 > 1 字面
    );
  });
});

describe("segments and splat", () => {
  it("matches trailing multi-segment splat (1+ segments)", () => {
    const r = compile(["/files/*path", "/files"]);
    const m = r.match("/files/a/b/c");
    assert.equal(m?.handler, "h0");
    assert.deepEqual(m?.params, { path: "a/b/c" });
    assert.equal(r.match("/files")?.handler, "h1"); // splat 不匹配空
  });

  it("matches root and strips query string", () => {
    const r = compile(["/", "/a"]);
    assert.equal(r.match("/")?.handler, "h0");
    assert.equal(r.match("/a?x=1&y=2")?.handler, "h1");
    assert.equal(r.match("/nope"), null);
  });

  it("supports optional groups mid-pattern", () => {
    const r = compile(["/a(/b)/c"]);
    assert.equal(r.match("/a/c")?.handler, "h0");
    assert.equal(r.match("/a/b/c")?.handler, "h0");
    assert.equal(r.match("/a/b"), null);
  });
});

describe("percent decoding", () => {
  it("decodes %2F inside a segment without re-splitting", () => {
    const r = compile(["/:a/:b", "/:one"]);
    const m = r.match("/x%2Fy");
    assert.equal(m?.handler, "h1"); // 仍是一段，命中单段路由
    assert.deepEqual(m?.params, { one: "x/y" });
  });

  it("decoded slash stays inside splat values", () => {
    const r = compile(["/f/*rest"]);
    const m = r.match("/f/a%2Fb/c");
    assert.deepEqual(m?.params, { rest: "a/b/c" });
  });

  it("matches encoded literals and survives malformed escapes", () => {
    const r = compile(["/a b", "/100%"]);
    assert.equal(r.match("/a%20b")?.handler, "h0");
    assert.equal(r.match("/100%")?.handler, "h1"); // 非法编码按原文匹配
  });

  it("splitPath decodes per segment", () => {
    assert.deepEqual(splitPath("/a%2Fb/c?d=e"), ["a/b", "c"]);
  });
});

describe("builder api", () => {
  it("chains add/compile", () => {
    const r = createRouter<string>()
      .add("/x", "X")
      .add("/y/:id", "Y")
      .compile();
    assert.equal(r.match("/x")?.handler, "X");
    assert.equal(r.match("/y/3")?.params.id, "3");
  });
});
