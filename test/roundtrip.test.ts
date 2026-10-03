import { test } from "node:test";
import assert from "node:assert/strict";
import { parse } from "../src/parser.ts";
import { prettyPrint } from "../src/printer.ts";
import {
  binary,
  bool,
  call,
  ident,
  num,
  str,
  unary,
  type BinOp,
  type Expr,
} from "../src/ast.ts";

const WIDTH = 80;

function assertRoundTrip(expr: Expr, width = WIDTH): string {
  const out = prettyPrint(expr, width);
  for (const line of out.split("\n")) {
    assert.ok(
      line.length <= width,
      `line exceeds ${width} columns (${line.length}): ${JSON.stringify(line)}`,
    );
  }
  const reparsed = parse(out);
  assert.deepEqual(reparsed, expr, `round-trip mismatch for:\n${out}`);
  return out;
}

test("round-trips a deeply nested hand-built AST", () => {
  const expr = call(ident("sigmoid"), [
    binary(
      "+",
      binary(
        "*",
        ident("weight_feature_cross"),
        call(ident("tanh"), [
          binary(
            "/",
            binary("-", ident("user_age_years"), num(12.5)),
            call(ident("sqrt"), [
              binary("+", ident("variance_age"), num(0.0001)),
            ]),
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
          binary(
            "==",
            binary("%", ident("experiment_group"), num(2)),
            num(0),
          ),
        ]),
      ),
    ),
  ]);
  const out = assertRoundTrip(expr);
  assert.ok(out.includes("\n"), "expected multi-line output");
});

test("round-trips right-nested operators with mandatory parens", () => {
  assertRoundTrip(binary("+", ident("a"), binary("+", ident("b"), ident("c"))));
  assertRoundTrip(binary("-", ident("a"), binary("+", ident("b"), ident("c"))));
  assertRoundTrip(binary("/", ident("a"), binary("/", ident("b"), ident("c"))));
  assertRoundTrip(
    binary("&&", ident("a"), binary("||", ident("b"), ident("c"))),
  );
  assertRoundTrip(unary("-", unary("-", ident("x"))));
  assertRoundTrip(call(call(ident("f"), [num(1)]), [num(2)]));
});

// --- property-based round-trip over randomly generated ASTs ---

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const BIN_OPS: BinOp[] = [
  "||", "&&", "==", "!=", "<", "<=", ">", ">=",
  "+", "-", "*", "/", "%",
];

function randomExpr(rand: () => number, depth: number): Expr {
  const pick = <T,>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  if (depth <= 0) {
    switch (Math.floor(rand() * 4)) {
      case 0:
        return num(Math.floor(rand() * 1000));
      case 1:
        return num(Math.round(rand() * 100) / 4);
      case 2:
        return str(`s${Math.floor(rand() * 100)}`);
      case 3:
        return bool(rand() < 0.5);
      default:
        return ident(`v${Math.floor(rand() * 1000)}`);
    }
  }
  switch (Math.floor(rand() * 4)) {
    case 0:
      return binary(
        pick(BIN_OPS),
        randomExpr(rand, depth - 1),
        randomExpr(rand, depth - 1),
      );
    case 1:
      return unary(rand() < 0.5 ? "-" : "!", randomExpr(rand, depth - 1));
    case 2: {
      const argc = Math.floor(rand() * 4);
      const args: Expr[] = [];
      for (let i = 0; i < argc; i++) args.push(randomExpr(rand, depth - 1));
      return call(ident(`fn${Math.floor(rand() * 100)}`), args);
    }
    default:
      return randomExpr(rand, 0);
  }
}

test("round-trips 500 randomly generated ASTs within 80 columns", () => {
  const rand = mulberry32(20261003);
  for (let i = 0; i < 500; i++) {
    const depth = 2 + Math.floor(rand() * 6); // depths 2..7
    assertRoundTrip(randomExpr(rand, depth));
  }
});

test("round-trips at narrower widths too", () => {
  const rand = mulberry32(42);
  for (let i = 0; i < 100; i++) {
    const depth = 2 + Math.floor(rand() * 5);
    const expr = randomExpr(rand, depth);
    const out = prettyPrint(expr, 40);
    assert.deepEqual(parse(out), expr);
  }
});
