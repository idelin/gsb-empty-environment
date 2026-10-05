import { test, assertEqual } from "./helpers.js";
import { canonicalKey, literalKey, makeRules } from "../src/index.js";

const rules = makeRules();

test("canonical: case folding + extension omission", () => {
  assertEqual(canonicalKey("Src/Foo.TS", rules), "src/foo");
  assertEqual(canonicalKey("src/foo.js", rules), "src/foo");
  assertEqual(canonicalKey("src/foo", rules), "src/foo");
});

test("canonical: index file omission", () => {
  assertEqual(canonicalKey("src/util/index.ts", rules), "src/util");
  assertEqual(canonicalKey("src/util", rules), "src/util");
  assertEqual(canonicalKey("src/util/INDEX.JS", rules), "src/util");
});

test("canonical: separators and dot segments are lexical, no fs dependence", () => {
  assertEqual(canonicalKey("src\\a\\..\\b\\c.ts", rules), "src/b/c");
  assertEqual(canonicalKey("./src//./b/c.ts", rules), "src/b/c");
  assertEqual(canonicalKey("../outside/x.ts", rules), "../outside/x");
});

test("canonical: alias longest prefix wins", () => {
  const r = makeRules({ aliases: { "@app": "src/app", "@app/util": "src/shared/util" } });
  assertEqual(canonicalKey("@app/util/index.ts", r), "src/shared/util");
  assertEqual(canonicalKey("@app/core/a.ts", r), "src/app/core/a");
});

test("canonical: caseSensitive option disables folding", () => {
  const r = makeRules({ caseSensitive: true });
  assertEqual(canonicalKey("Src/Foo.ts", r), "Src/Foo");
});

test("canonical: custom extension list", () => {
  const r = makeRules({ extensions: [".vue", "css"] });
  assertEqual(canonicalKey("src/Comp.VUE", r), "src/comp");
  assertEqual(canonicalKey("src/a.css", r), "src/a");
  assertEqual(canonicalKey("src/a.ts", r), "src/a.ts");
});

test("literal: no folding, no extension strip, alias expanded", () => {
  assertEqual(literalKey("src/./Foo.ts", rules), "src/Foo.ts");
  const r = makeRules({ aliases: { "@app": "src/app" } });
  assertEqual(literalKey("@app/Foo.ts", r), "src/app/Foo.ts");
});
