/**
 * Demo: build a deeply nested expression AST, pretty-print it within
 * 80 columns, then re-parse the output and verify the AST is identical.
 */
import assert from "node:assert/strict";
import {
  binary,
  bool,
  call,
  ident,
  num,
  parse,
  prettyPrint,
  str,
  unary,
  type Expr,
} from "../src/index.ts";

const expr: Expr = call(ident("sigmoid"), [
  binary(
    "+",
    binary(
      "*",
      ident("weight_feature_cross"),
      call(ident("tanh"), [
        binary(
          "/",
          binary("-", ident("user_age_years"), num(12.5)),
          call(ident("sqrt"), [binary("+", ident("variance_age"), num(0.0001))]),
        ),
      ]),
    ),
    binary(
      "*",
      call(ident("lookup_embedding"), [
        str("user_segment"),
        call(ident("hash_bucket"), [ident("user_id"), num(64)]),
      ]),
      call(ident("gate"), [
        binary("&&", ident("is_active"), unary("!", ident("is_blocked"))),
        binary("||", ident("has_coupon"), binary(">", ident("cart_total"), num(99))),
        binary("==", binary("%", ident("experiment_group"), num(2)), num(0)),
        bool(true),
      ]),
    ),
  ),
]);

const WIDTH = 80;
const printed = prettyPrint(expr, WIDTH);

console.log(`--- pretty-printed (width ${WIDTH}) ---`);
console.log(printed);
console.log("-".repeat(WIDTH));

const lines = printed.split("\n");
const maxLine = Math.max(...lines.map((l) => l.length));
console.log(`lines: ${lines.length}, longest line: ${maxLine} columns`);

const reparsed = parse(printed);
assert.deepEqual(reparsed, expr);
console.log("round-trip OK: re-parsed AST is structurally identical");
