import { strict as assert } from "node:assert";
import { checkReplacement } from "../src/check.js";
import { renderReport, renderShape } from "../src/render.js";
import { bool, field, func, nil, num, optField, optParam, param, rec, ref, str, union, undef } from "../src/builders.js";
import type { Env, Type } from "../src/types.js";

let failures = 0;

function expectCompatible(title: string, newType: Type, oldType: Type, env: Env): void {
  const result = checkReplacement(newType, oldType, env);
  if (!result.compatible) {
    failures += 1;
    console.error("FAIL (expected compatible): " + title);
    console.error(renderReport(result));
    return;
  }
  console.log("ok: " + title);
}

function expectIncompatible(
  title: string,
  newType: Type,
  oldType: Type,
  env: Env,
  pathPart: string,
  witnessPart: string,
): void {
  const result = checkReplacement(newType, oldType, env);
  if (result.compatible) {
    failures += 1;
    console.error("FAIL (expected incompatible): " + title);
    return;
  }
  const first = result.problems[0];
  const witness = renderShape(first.witness);
  if (!first.path.includes(pathPart) || !witness.includes(witnessPart)) {
    failures += 1;
    console.error("FAIL (wrong path/witness): " + title);
    console.error("  wanted path to contain: " + pathPart + ", got: " + first.path);
    console.error("  wanted witness to contain: " + witnessPart + ", got: " + witness);
    return;
  }
  console.log("ok: " + title);
}

const empty: Env = new Map();

// primitives
expectCompatible("identical primitives", num, num, empty);
expectIncompatible("primitive mismatch", num, str, empty, "$", "0");

// records: width, required/optional, nullable vs optional
expectCompatible("width: extra fields are fine", rec(field("a", num), field("b", str)), rec(field("a", num)), empty);
expectIncompatible("missing required field", rec(field("a", num)), rec(field("a", num), field("b", str)), empty, "$.b", "{ a: 0 }");
expectIncompatible("optional cannot replace required", rec(optField("a", num)), rec(field("a", num)), empty, "$.a", "{}");
expectCompatible("required satisfies optional", rec(field("a", num)), rec(optField("a", num)), empty);
expectIncompatible("nullable is not optional", rec(field("a", union(num, undef))), rec(optField("a", num)), empty, "$.a", "undefined");
expectIncompatible("optional is not nullable", rec(optField("a", num)), rec(field("a", union(num, undef))), empty, "$.a", "{}");

// unions
expectCompatible("union narrows", union(num, str), union(num, str, bool), empty);
expectIncompatible("union widens", union(num, str, bool), union(num, str), empty, "$|#2", "true");
expectCompatible("record matches a union branch", rec(field("a", num)), union(rec(field("a", num)), rec(field("b", str))), empty);
expectIncompatible("no union branch matches", bool, union(num, str), empty, "$", "true");

// functions: variance and arity
const oldHandler = rec(field("handle", func(
  [param("e", rec(field("code", num), field("msg", str)))],
  rec(field("ok", bool)),
)));
expectCompatible(
  "param contravariance + return covariance",
  rec(field("handle", func([param("e", rec(field("code", num)))], rec(field("ok", bool), field("detail", str))))),
  oldHandler,
  empty,
);
expectIncompatible(
  "param contravariance violated",
  rec(field("handle", func([param("e", rec(field("code", num), field("msg", str), field("trace", str)))], rec(field("ok", bool))))),
  oldHandler,
  empty,
  ".arg0(e)",
  "{ code: 0",
);
expectIncompatible(
  "return covariance violated",
  rec(field("handle", func([param("e", rec(field("code", num), field("msg", str)))], rec()))),
  oldHandler,
  empty,
  ".return.ok",
  "(…) => {}",
);
expectCompatible("fewer parameters are fine", rec(field("f", func([], num))), rec(field("f", func([param("x", num)], num))), empty);
expectIncompatible(
  "more required parameters are not",
  rec(field("f", func([param("x", num), param("y", str)], num))),
  rec(field("f", func([param("x", num)], num))),
  empty,
  ".arg1(y)",
  "\"s\"",
);
expectIncompatible(
  "required param cannot replace optional param",
  rec(field("f", func([param("x", num)], num))),
  rec(field("f", func([optParam("x", num)], num))),
  empty,
  ".arg0(x)",
  "0",
);

// recursive types: termination through pairwise visited state
const listEnv: Env = new Map([
  ["OldList", rec(field("head", num), field("tail", union(ref("OldList"), nil)))],
  ["NewList", rec(field("head", num), field("tail", union(ref("NewList"), nil)), field("size", num))],
]);
expectCompatible("recursive list: new adds a field", ref("NewList"), ref("OldList"), listEnv);

const badListEnv: Env = new Map([
  ["OldList", rec(field("head", num), field("tail", union(ref("OldList"), nil)))],
  ["NewList", rec(field("head", str), field("tail", union(ref("NewList"), nil)))],
]);
expectIncompatible("recursive list: head mismatch", ref("NewList"), ref("OldList"), badListEnv, "$.head", "\"s\"");

const treeEnv: Env = new Map([
  ["OldTree", rec(field("v", num), field("kid", union(ref("OldTree"), nil)))],
  ["NewTree", rec(field("v", num), field("kid", union(ref("NewTree"), bool)))],
]);
expectIncompatible("recursive tree: leaf mismatch terminates", ref("NewTree"), ref("OldTree"), treeEnv, "$.kid|#1", "true");

const mutualEnv: Env = new Map([
  ["OldA", rec(field("b", ref("OldB")))],
  ["OldB", rec(field("a", union(ref("OldA"), nil)))],
  ["NewA", rec(field("b", ref("NewB")))],
  ["NewB", rec(field("a", union(ref("NewA"), nil)))],
]);
expectCompatible("mutually recursive types", ref("NewA"), ref("OldA"), mutualEnv);

// sanity: reports render
const report = checkReplacement(num, str, empty);
assert.equal(report.compatible, false);
assert.ok(renderReport(report).includes("counterexample"));

if (failures > 0) {
  console.error(failures + " test(s) failed");
  process.exit(1);
}
console.log("all tests passed");
