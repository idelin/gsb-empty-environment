import { describe, expect, test } from "bun:test";
import {
  canonicalKey,
  compileAliases,
  normalizePath,
  relativePath,
  stripKnownExtensions,
} from "../src/pathRules";

const EXTS = [".ts", ".js"];

describe("normalizePath（纯词法，不依赖宿主文件系统）", () => {
  test("统一分隔符并折叠 . 与 ..", () => {
    expect(normalizePath("src\\util\\..\\Foo.ts")).toBe("src/Foo.ts");
    expect(normalizePath("a//b/./c/")).toBe("a/b/c");
    expect(normalizePath("/a/b/../c")).toBe("/a/c");
  });
  test("相对路径越界时保留 ..", () => {
    expect(normalizePath("../a/../b")).toBe("../b");
  });
});

describe("canonicalKey（大小写折叠 + 扩展名省略 + 别名）", () => {
  const aliases = compileAliases({ "@app/*": "src/*" });
  test("大小写折叠", () => {
    expect(canonicalKey("src/Foo.TS", [], EXTS)).toBe("src/foo");
  });
  test("扩展名省略", () => {
    expect(canonicalKey("src/foo.js", [], EXTS)).toBe(canonicalKey("src/foo", [], EXTS));
  });
  test("别名展开后等价", () => {
    expect(canonicalKey("@app/util/Bar.ts", aliases, EXTS)).toBe("src/util/bar");
  });
});

test("stripKnownExtensions 反复剥离已知扩展名", () => {
  expect(stripKnownExtensions("a.d.ts", [".ts"])).toBe("a.d");
  expect(stripKnownExtensions("a.test.ts", [".ts", ".test"])).toBe("a");
});

test("relativePath 词法相对路径", () => {
  expect(relativePath("src", "src/foo.ts")).toBe("./foo.ts");
  expect(relativePath("src/a", "src/b/c.ts")).toBe("../b/c.ts");
});
