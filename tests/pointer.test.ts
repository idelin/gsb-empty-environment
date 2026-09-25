import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  compilePointer,
  getByPointer,
  matchPattern,
  parsePointer,
  patternsOverlap,
  representativePattern,
  setByPointer,
  walkLeaves,
} from "../src/pointer.ts";

describe("JSON Pointer", () => {
  it("编译/解析往返并处理转义", () => {
    assert.equal(compilePointer(["a", "b/c", "d~e"]), "/a/b~1c/d~0e");
    assert.deepEqual(parsePointer("/a/b~1c/d~0e"), ["a", "b/c", "d~e"]);
    assert.equal(compilePointer([]), "");
    assert.deepEqual(parsePointer(""), []);
  });

  it("拒绝非法指针与非法转义", () => {
    assert.throws(() => parsePointer("a/b"), /非法 JSON Pointer/);
    assert.throws(() => parsePointer("/a~2"), /非法 JSON Pointer 转义/);
  });

  it("模式匹配：单段通配，不跨段", () => {
    assert.equal(matchPattern("/items/*/name", "/items/3/name"), true);
    assert.equal(matchPattern("/items/*/name", "/items/3/note"), false);
    assert.equal(matchPattern("/items/*/name", "/items/3/name/x"), false);
    assert.equal(matchPattern("/a/*", "/a/b"), true);
    assert.equal(matchPattern("/a/*", "/a/b/c"), false);
  });

  it("模式相交判断与代表模式", () => {
    assert.equal(patternsOverlap("/items/*/name", "/items/2/name"), true);
    assert.equal(patternsOverlap("/a/x", "/a/y"), false);
    assert.equal(patternsOverlap("/a/*/x", "/a/b/y"), false);
    assert.equal(representativePattern("/items/*/name"), "/items/0/name");
  });

  it("叶子遍历：数组有序、对象稳定，含 null 叶子", () => {
    const tree = { b: [1, { c: null }], a: "x" };
    const paths: string[] = [];
    walkLeaves(tree, (p, _s, v) => paths.push(`${p}=${v === null ? "null" : JSON.stringify(v)}`));
    assert.deepEqual(paths, [
      "/a=\"x\"",
      "/b/0=1",
      "/b/1/c=null",
    ]);
  });

  it("取值与设值", () => {
    const tree = { a: [{ b: "v" }] };
    assert.equal(getByPointer(tree, "/a/0/b"), "v");
    assert.equal(getByPointer(tree, "/a/2/b"), undefined);
    setByPointer(tree, "/a/0/b", "w");
    assert.equal(getByPointer(tree, "/a/0/b"), "w");
  });
});
