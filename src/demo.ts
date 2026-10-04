import { checkReplacement } from "./check.js";
import { renderReport } from "./render.js";
import { bool, field, func, nil, num, optField, optParam, param, rec, ref, str, union, undef } from "./builders.js";
import type { Env, Type } from "./types.js";

function show(title: string, newType: Type, oldType: Type, env: Env): void {
  console.log("=== " + title + " ===");
  console.log(renderReport(checkReplacement(newType, oldType, env)));
  console.log();
}

const empty: Env = new Map();

// 1. Recursive linked lists: the new list adds a field -> compatible.
const listEnv: Env = new Map([
  ["OldList", rec(field("head", num), field("tail", union(ref("OldList"), nil)))],
  ["NewList", rec(field("head", num), field("tail", union(ref("NewList"), nil)), field("size", num))],
]);
show("recursive list: new adds a field", ref("NewList"), ref("OldList"), listEnv);

// 2. Same lists, but the new one changed head to string -> incompatible.
const badListEnv: Env = new Map([
  ["OldList", rec(field("head", num), field("tail", union(ref("OldList"), nil)))],
  ["NewList", rec(field("head", str), field("tail", union(ref("NewList"), nil)))],
]);
show("recursive list: head type changed", ref("NewList"), ref("OldList"), badListEnv);

// 3. Optional is not nullable, in both directions.
show(
  "optional != nullable: new makes the field nullable",
  rec(field("a", union(num, undef))),
  rec(optField("a", num)),
  empty,
);
show(
  "optional != nullable: new makes the field optional",
  rec(optField("a", num)),
  rec(field("a", num)),
  empty,
);
show(
  "a required field satisfies an optional one",
  rec(field("a", num)),
  rec(optField("a", num)),
  empty,
);

// 4. Function variance.
const oldHandler = rec(field("handle", func(
  [param("e", rec(field("code", num), field("msg", str)))],
  rec(field("ok", bool)),
)));
show(
  "parameter contravariance violated: new handler demands more",
  rec(field("handle", func(
    [param("e", rec(field("code", num), field("msg", str), field("trace", str)))],
    rec(field("ok", bool)),
  ))),
  oldHandler,
  empty,
);
show(
  "parameter contravariance + return covariance respected",
  rec(field("handle", func(
    [param("e", rec(field("code", num)))],
    rec(field("ok", bool), field("detail", str)),
  ))),
  oldHandler,
  empty,
);
show(
  "new function may ignore parameters",
  rec(field("f", func([], num))),
  rec(field("f", func([param("x", num)], num))),
  empty,
);
show(
  "new function may not require more parameters",
  rec(field("f", func([param("x", num), param("y", str)], num))),
  rec(field("f", func([param("x", num)], num))),
  empty,
);
show(
  "required parameter cannot replace an optional one",
  rec(field("f", func([param("x", num)], num))),
  rec(field("f", func([optParam("x", num)], num))),
  empty,
);

// 5. Recursive trees with a mismatched leaf: comparison terminates via the
// pairwise visited state and still reports the deep problem.
const treeEnv: Env = new Map([
  ["OldTree", rec(field("v", num), field("kid", union(ref("OldTree"), nil)))],
  ["NewTree", rec(field("v", num), field("kid", union(ref("NewTree"), bool)))],
]);
show("recursive trees: mismatched leaf", ref("NewTree"), ref("OldTree"), treeEnv);

// 6. Unions: narrowing is safe, widening is not.
show("union narrows", union(num, str), union(num, str, bool), empty);
show("union widens", union(num, str, bool), union(num, str), empty);
