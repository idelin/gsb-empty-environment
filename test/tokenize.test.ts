import { describe, expect, test } from "bun:test";
import { tokenize } from "../src/tokenize";

describe("tokenize", () => {
  test("splits on whitespace and punctuation, lowercases", () => {
    expect(tokenize("  Hello,   WORLD! foo-bar/baz")).toEqual([
      "hello",
      "world",
      "foo",
      "bar",
      "baz",
    ]);
  });

  test("dedupes tokens keeping first position", () => {
    expect(tokenize("Beta alpha beta ALPHA gamma")).toEqual([
      "beta",
      "alpha",
      "gamma",
    ]);
  });

  test("keeps CJK runs and digits as tokens", () => {
    expect(tokenize("搜索 状态库 v2 第3版")).toEqual([
      "搜索",
      "状态库",
      "v2",
      "第3版",
    ]);
  });

  test("returns no tokens for blank or punctuation-only input", () => {
    expect(tokenize("   ")).toEqual([]);
    expect(tokenize("…—…")).toEqual([]);
  });
});
