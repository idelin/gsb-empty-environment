import { test } from "node:test";
import assert from "node:assert/strict";
import { prettyPrint } from "../src/printer.ts";
import { binary, call, ident, num, str, unary, type Expr } from "../src/ast.ts";

const WIDTH = 80;

function assertWithinWidth(out: string, width = WIDTH): void {
  for (const line of out.split("\n")) {
    assert.ok(
      line.length <= width,
      `line exceeds ${width} columns (${line.length}): ${JSON.stringify(line)}`,
    );
  }
}

test("short expressions stay on one line", () => {
  assert.equal(prettyPrint(binary("+", ident("a"), ident("b"))), "a + b");
  assert.equal(
    prettyPrint(call(ident("f"), [num(1), num(2)])),
    "f(1, 2)",
  );
});

test("adds parentheses required by precedence and associativity", () => {
  assert.equal(
    prettyPrint(binary("*", binary("+", ident("a"), ident("b")), ident("c"))),
    "(a + b) * c",
  );
  assert.equal(
    prettyPrint(binary("+", ident("a"), binary("+", ident("b"), ident("c")))),
    "a + (b + c)",
  );
  assert.equal(
    prettyPrint(binary("-", ident("a"), binary("-", ident("b"), ident("c")))),
    "a - (b - c)",
  );
  assert.equal(
    prettyPrint(unary("-", binary("+", ident("a"), ident("b")))),
    "-(a + b)",
  );
  assert.equal(prettyPrint(unary("-", unary("-", ident("x")))), "-(-x)");
});

test("wraps long operator chains at operators with indentation", () => {
  let expr: Expr = ident("value_00");
  for (let i = 1; i < 12; i++) {
    expr = binary("+", expr, ident(`value_${String(i).padStart(2, "0")}`));
  }
  const out = prettyPrint(expr);
  const lines = out.split("\n");
  assert.ok(lines.length > 1, "expected the chain to wrap");
  assertWithinWidth(out);
  for (const line of lines.slice(1)) {
    assert.match(line, /^ {2}\+ /, `continuation should be indented operator: ${line}`);
  }
});

test("wraps long call argument lists at commas", () => {
  const expr = call(ident("compute_weighted_score"), [
    ident("first_argument_value"),
    ident("second_argument_value"),
    ident("third_argument_value"),
    ident("fourth_argument_value"),
  ]);
  const out = prettyPrint(expr);
  const lines = out.split("\n");
  assert.ok(lines.length > 1, "expected the call to wrap");
  assertWithinWidth(out);
  assert.equal(lines[0], "compute_weighted_score(");
  assert.equal(lines[1], "  first_argument_value,");
  assert.equal(lines.at(-1), ")");
});

test("nested groups break outermost-first", () => {
  // The whole call does not fit, but the inner binary fits on its own line.
  const expr = call(ident("outer_function"), [
    binary(
      "+",
      binary("*", ident("alpha_value"), ident("beta_value")),
      binary("*", ident("gamma_value"), ident("delta_value")),
    ),
    ident("extra"),
  ]);
  const out = prettyPrint(expr, 60);
  assert.equal(
    out,
    [
      "outer_function(",
      "  alpha_value * beta_value + gamma_value * delta_value,",
      "  extra",
      ")",
    ].join("\n"),
  );
  assertWithinWidth(out, 60);
});

test("deeply nested mixed expression stays within 80 columns", () => {
  const expr = call(ident("sigmoid"), [
    binary(
      "+",
      binary(
        "*",
        ident("weight_matrix_row"),
        call(ident("tanh"), [
          binary(
            "/",
            binary("-", ident("user_age_years"), num(12.5)),
            call(ident("sqrt"), [
              binary("+", ident("age_variance"), num(0.0001)),
            ]),
          ),
        ]),
      ),
      call(ident("bias_term"), [str("layer_one"), num(3)]),
    ),
  ]);
  const out = prettyPrint(expr);
  assert.ok(out.includes("\n"), "expected wrapping");
  assertWithinWidth(out);
});
