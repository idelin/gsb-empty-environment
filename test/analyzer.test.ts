import { describe, expect, test } from "bun:test";
import { analyze } from "../src/analyzer";

describe("冲突检测", () => {
  test("大小写折叠冲突", () => {
    const r = analyze({
      modules: [{ path: "src/Foo.ts" }, { path: "src/foo.ts" }],
    });
    expect(r.conflicts).toEqual([
      { key: "src/foo", modules: ["src/Foo.ts", "src/foo.ts"] },
    ]);
  });

  test("扩展名省略冲突", () => {
    const r = analyze({
      modules: [{ path: "src/a.ts" }, { path: "src/a.js" }],
    });
    expect(r.conflicts.map((c) => c.key)).toEqual(["src/a"]);
  });

  test("别名导致的冲突", () => {
    const r = analyze({
      aliases: { "@app/*": "src/*" },
      modules: [{ path: "@app/util/x.ts" }, { path: "src/util/X.ts" }],
    });
    expect(r.conflicts).toEqual([
      { key: "src/util/x", modules: ["src/util/x.ts", "src/util/X.ts"] },
    ]);
  });

  test("无冲突时结果为空", () => {
    const r = analyze({ modules: [{ path: "a/x.ts" }, { path: "a/y.ts" }] });
    expect(r.conflicts).toEqual([]);
    expect(r.renames).toEqual([]);
    expect(r.totalCost).toBe(0);
  });
});

describe("最小代价改名", () => {
  test("保留代价最高者，总代价最小", () => {
    const r = analyze({
      modules: [
        { path: "src/Foo.ts", renameCost: 10 },
        { path: "src/foo.ts", renameCost: 3 },
        { path: "src/FOO.js", renameCost: 5 },
      ],
    });
    expect(r.totalCost).toBe(8);
    expect(r.renames).toEqual([
      { from: "src/FOO.js", to: "src/FOO-2.js", cost: 5 },
      { from: "src/foo.ts", to: "src/foo-3.ts", cost: 3 },
    ]);
  });

  test("缺省代价为 1，并列时保留输入靠前者", () => {
    const r = analyze({ modules: [{ path: "a/X.ts" }, { path: "a/x.ts" }] });
    expect(r.renames).toEqual([{ from: "a/x.ts", to: "a/x-2.ts", cost: 1 }]);
  });

  test("新名字避开已占用的键", () => {
    const r = analyze({
      modules: [
        { path: "a/Foo.ts", renameCost: 9 },
        { path: "a/foo.ts" },
        { path: "a/foo-2.ts" },
      ],
    });
    expect(r.renames).toEqual([{ from: "a/foo.ts", to: "a/foo-3.ts", cost: 1 }]);
  });
});

describe("引用同步更新", () => {
  test("相对引用（省略扩展名风格保持）", () => {
    const r = analyze({
      modules: [
        { path: "src/Foo.ts", renameCost: 10 },
        { path: "src/foo.ts", renameCost: 1 },
        { path: "src/main.ts" },
      ],
      references: [{ from: "src/main.ts", specifier: "./foo" }],
    });
    // "./foo" 大小写敏感精确匹配到 src/foo.ts（被改名者）。
    expect(r.updatedReferences).toEqual([
      {
        from: "src/main.ts",
        oldSpecifier: "./foo",
        newSpecifier: "./foo-2",
        resolvesTo: "src/foo-2.ts",
      },
    ]);
  });

  test("相对引用（带扩展名风格保持）", () => {
    const r = analyze({
      modules: [
        { path: "src/Foo.ts", renameCost: 10 },
        { path: "src/foo.ts", renameCost: 1 },
        { path: "src/deep/main.ts" },
      ],
      references: [{ from: "src/deep/main.ts", specifier: "../foo.ts" }],
    });
    expect(r.updatedReferences[0].newSpecifier).toBe("../foo-2.ts");
  });

  test("别名引用同步更新", () => {
    const r = analyze({
      aliases: { "@app/*": "src/*" },
      modules: [
        { path: "src/util/Helper.ts", renameCost: 10 },
        { path: "src/util/helper.ts", renameCost: 1 },
        { path: "src/main.ts" },
      ],
      references: [{ from: "src/main.ts", specifier: "@app/util/helper" }],
    });
    expect(r.updatedReferences[0]).toMatchObject({
      newSpecifier: "@app/util/helper-2",
      resolvesTo: "src/util/helper-2.ts",
    });
  });

  test("指向未改名模块的引用不变", () => {
    const r = analyze({
      modules: [
        { path: "src/Foo.ts", renameCost: 10 },
        { path: "src/foo.ts", renameCost: 1 },
        { path: "src/main.ts" },
      ],
      references: [{ from: "src/main.ts", specifier: "./Foo" }],
    });
    expect(r.updatedReferences).toEqual([]);
  });

  test("无法解析与歧义引用被报告", () => {
    const r = analyze({
      modules: [
        { path: "src/Foo.ts" },
        { path: "src/foo.ts" },
        { path: "src/main.ts" },
      ],
      references: [
        { from: "src/main.ts", specifier: "./missing" },
        { from: "src/main.ts", specifier: "./FOO" },
      ],
    });
    expect(r.badReferences).toContainEqual({
      from: "src/main.ts",
      specifier: "./missing",
      reason: "unresolved",
    });
    // "./FOO" 与两个模块都只是折叠后相等，无唯一精确匹配 -> 歧义。
    const ambiguous = r.badReferences.find((b) => b.specifier === "./FOO");
    expect(ambiguous?.reason).toBe("ambiguous");
    expect(ambiguous?.candidates?.sort()).toEqual(["src/Foo.ts", "src/foo.ts"]);
  });

  test("外部包说明符被忽略", () => {
    const r = analyze({
      modules: [{ path: "src/main.ts" }],
      references: [{ from: "src/main.ts", specifier: "lodash" }],
    });
    expect(r.badReferences).toEqual([]);
    expect(r.updatedReferences).toEqual([]);
  });
});
