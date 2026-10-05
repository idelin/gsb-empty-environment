import { test, assertEqual } from "./helpers.js";
import { analyzeBundle } from "../src/index.js";

test("conflict: case folding group, minimal rename keeps most expensive", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/Foo.ts", renameCost: 5 },
      { path: "src/foo.js", renameCost: 2 },
      { path: "src/FOO.mjs", renameCost: 3 },
    ],
  });
  assertEqual(report.conflicts, [{ key: "src/foo", modules: ["src/FOO.mjs", "src/Foo.ts", "src/foo.js"] }]);
  assertEqual(report.renames, [
    { from: "src/foo.js", to: "src/foo_2.js", cost: 2 },
    { from: "src/FOO.mjs", to: "src/FOO_3.mjs", cost: 3 },
  ]);
  assertEqual(report.totalCost, 5);
  assertEqual(report.ok, true);
});

test("conflict: index file vs extension omission", () => {
  const report = analyzeBundle({
    modules: ["src/util/index.ts", "src/util.ts"],
  });
  assertEqual(report.conflicts, [{ key: "src/util", modules: ["src/util.ts", "src/util/index.ts"] }]);
  assertEqual(report.renames, [{ from: "src/util/index.ts", to: "src/util/index_2.ts", cost: 1 }]);
  assertEqual(report.totalCost, 1);
});

test("conflict: candidate names avoid existing modules", () => {
  const report = analyzeBundle({
    modules: ["src/foo.ts", "src/foo.js", "src/foo_2.ts"],
  });
  assertEqual(report.renames, [{ from: "src/foo.ts", to: "src/foo_3.ts", cost: 1 }]);
});

test("conflict: alias equivalence detected", () => {
  const report = analyzeBundle({
    modules: ["src/app/Main.ts", "@app/main.ts"],
    options: { aliases: { "@app": "src/app" } },
  });
  assertEqual(report.conflicts, [{ key: "src/app/main", modules: ["@app/main.ts", "src/app/Main.ts"] }]);
  assertEqual(report.renames, [{ from: "src/app/Main.ts", to: "src/app/Main_2.ts", cost: 1 }]);
});

test("duplicates: lexically identical paths reported separately", () => {
  const report = analyzeBundle({
    modules: ["src/a.ts", "src/./a.ts", "src\\a.ts"],
  });
  assertEqual(report.duplicates, [["src/./a.ts", "src/a.ts", "src\\a.ts"]]);
  assertEqual(report.conflicts, []);
  assertEqual(report.ok, false);
});

test("duplicates: alias-expanded identical paths are duplicates", () => {
  const report = analyzeBundle({
    modules: ["@app/main.ts", "src/app/main.ts"],
    options: { aliases: { "@app": "src/app" } },
  });
  assertEqual(report.duplicates, [["@app/main.ts", "src/app/main.ts"]]);
  assertEqual(report.conflicts, []);
});

test("rename: default cost is 1 and keeper tie-break is lexicographic", () => {
  const report = analyzeBundle({
    modules: ["src/x.ts", "src/X.ts"],
  });
  assertEqual(report.renames, [{ from: "src/x.ts", to: "src/x_2.ts", cost: 1 }]);
});

test("rename: invalid cost rejected", () => {
  let threw = false;
  try {
    analyzeBundle({ modules: [{ path: "a.ts", renameCost: -1 }] });
  } catch (err) {
    threw = true;
  }
  assertEqual(threw, true);
});
