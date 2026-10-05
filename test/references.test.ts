import { test, assertEqual } from "./helpers.js";
import { analyzeBundle } from "../src/index.js";

test("references: exact-match specifier to renamed module is rewritten", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/Foo.ts", renameCost: 5 },
      { path: "src/foo.js", renameCost: 1 },
      { path: "src/main.ts" },
    ],
    references: [{ from: "src/main.ts", specifier: "./foo.js" }],
  });
  assertEqual(report.references, [{
    from: "src/main.ts",
    newFrom: "src/main.ts",
    specifier: "./foo.js",
    newSpecifier: "./foo_2.js",
    resolvesTo: "src/foo_2.js",
    candidates: [],
    status: "updated",
  }]);
  assertEqual(report.ok, true);
});

test("references: bare name hitting a conflict group is ambiguous", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/Foo.ts", renameCost: 5 },
      { path: "src/foo.js", renameCost: 1 },
      { path: "src/main.ts" },
    ],
    references: [{ from: "src/main.ts", specifier: "./Foo" }],
  });
  const ref = report.references[0];
  assertEqual(ref.status, "ambiguous");
  assertEqual(ref.candidates, ["src/Foo.ts", "src/foo.js"]);
  assertEqual(ref.newSpecifier, null);
  assertEqual(report.ok, false);
});

test("references: unresolved and external specifiers are reported", () => {
  const report = analyzeBundle({
    modules: ["src/main.ts"],
    references: [
      { from: "src/main.ts", specifier: "./missing" },
      { from: "src/main.ts", specifier: "lodash" },
    ],
  });
  assertEqual(report.references[0].status, "unresolved");
  assertEqual(report.references[1].status, "external");
  assertEqual(report.ok, false);
});

test("references: alias specifier rebuilt after rename", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/app/util/Helper.ts", renameCost: 10 },
      { path: "src/app/util/helper.tsx", renameCost: 1 },
    ],
    options: { aliases: { "@app": "src/app" } },
    references: [{ from: "src/app/main.ts", specifier: "@app/util/helper.tsx" }],
  });
  assertEqual(report.renames, [{ from: "src/app/util/helper.tsx", to: "src/app/util/helper_2.tsx", cost: 1 }]);
  const ref = report.references[0];
  assertEqual(ref.status, "updated");
  assertEqual(ref.newSpecifier, "@app/util/helper_2.tsx");
  assertEqual(ref.resolvesTo, "src/app/util/helper_2.tsx");
});

test("references: relative specifier across directories", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/a/Foo.ts", renameCost: 5 },
      { path: "src/a/foo.js", renameCost: 1 },
      { path: "src/b/main.ts" },
    ],
    references: [{ from: "src/b/main.ts", specifier: "../a/foo.js" }],
  });
  const ref = report.references[0];
  assertEqual(ref.status, "updated");
  assertEqual(ref.newSpecifier, "../a/foo_2.js");
});

test("references: extension style of original specifier is preserved", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/deep/Data.json", renameCost: 9 },
      { path: "src/deep/data.JSON", renameCost: 1 },
      { path: "src/main.ts" },
    ],
    references: [{ from: "src/main.ts", specifier: "./deep/data.JSON" }],
  });
  const ref = report.references[0];
  assertEqual(ref.status, "updated");
  assertEqual(ref.newSpecifier, "./deep/data_2.JSON");
});

test("references: ref to kept module stays ok", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/Foo.ts", renameCost: 5 },
      { path: "src/foo.js", renameCost: 1 },
      { path: "src/main.ts" },
    ],
    references: [{ from: "src/main.ts", specifier: "./Foo.ts" }],
  });
  const ref = report.references[0];
  assertEqual(ref.status, "ok");
  assertEqual(ref.newSpecifier, null);
  assertEqual(ref.resolvesTo, "src/Foo.ts");
});

test("references: importer moved by rename keeps working specifier", () => {
  const report = analyzeBundle({
    modules: [
      { path: "src/util/index.ts", renameCost: 1 },
      { path: "src/util.ts", renameCost: 2 },
      { path: "src/util/helper.ts" },
    ],
    references: [{ from: "src/util/index.ts", specifier: "./helper" }],
  });
  const ref = report.references[0];
  assertEqual(ref.newFrom, "src/util/index_2.ts");
  assertEqual(ref.status, "ok");
  assertEqual(ref.newSpecifier, null);
  assertEqual(ref.resolvesTo, "src/util/helper.ts");
});
