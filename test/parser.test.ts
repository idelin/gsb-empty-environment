import { test } from "node:test";
import assert from "node:assert/strict";
import { parse } from "../src/parser.ts";
import { binary, bool, call, ident, num, str, unary } from "../src/ast.ts";

test("parses literals and identifiers", () => {
  assert.deepEqual(parse("42"), num(42));
  assert.deepEqual(parse("3.25"), num(3.25));
  assert.deepEqual(parse("1e+3"), num(1000));
  assert.deepEqual(parse('"hello"'), str("hello"));
  assert.deepEqual(parse("true"), bool(true));
  assert.deepEqual(parse("false"), bool(false));
  assert.deepEqual(parse("alpha_beta"), ident("alpha_beta"));
});

test("parses string escapes", () => {
  assert.deepEqual(parse('"a\\nb\\t\\"q\\""'), str('a\nb\t"q"'));
});

test("respects operator precedence", () => {
  assert.deepEqual(
    parse("a + b * c"),
    binary("+", ident("a"), binary("*", ident("b"), ident("c"))),
  );
  assert.deepEqual(
    parse("a * b + c"),
    binary("+", binary("*", ident("a"), ident("b")), ident("c")),
  );
  assert.deepEqual(
    parse("a && b || c"),
    binary("||", binary("&&", ident("a"), ident("b")), ident("c")),
  );
  assert.deepEqual(
    parse("a < b == c"),
    binary("==", binary("<", ident("a"), ident("b")), ident("c")),
  );
});

test("binary operators are left-associative", () => {
  assert.deepEqual(
    parse("a - b - c"),
    binary("-", binary("-", ident("a"), ident("b")), ident("c")),
  );
});

test("parentheses override precedence", () => {
  assert.deepEqual(
    parse("(a + b) * c"),
    binary("*", binary("+", ident("a"), ident("b")), ident("c")),
  );
});

test("parses unary operators", () => {
  assert.deepEqual(parse("-x"), unary("-", ident("x")));
  assert.deepEqual(parse("!flag"), unary("!", ident("flag")));
  assert.deepEqual(
    parse("-(a + b)"),
    unary("-", binary("+", ident("a"), ident("b"))),
  );
  assert.deepEqual(
    parse("-a * b"),
    binary("*", unary("-", ident("a")), ident("b")),
  );
});

test("parses calls with arguments", () => {
  assert.deepEqual(parse("f()"), call(ident("f"), []));
  assert.deepEqual(
    parse("f(1, 2 + 3)"),
    call(ident("f"), [num(1), binary("+", num(2), num(3))]),
  );
  assert.deepEqual(
    parse("f(1)(2)"),
    call(call(ident("f"), [num(1)]), [num(2)]),
  );
});

test("rejects invalid input", () => {
  assert.throws(() => parse("a +"), SyntaxError);
  assert.throws(() => parse("(a"), SyntaxError);
  assert.throws(() => parse("a b"), SyntaxError);
  assert.throws(() => parse("f(1,)"), SyntaxError);
  assert.throws(() => parse(""), SyntaxError);
});
