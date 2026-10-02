import { test } from "node:test";
import assert from "node:assert/strict";
import { Rat } from "../src/rational.ts";

test("parses fractions, decimals, integers, exponents exactly", () => {
  assert.equal(Rat.from("3/4").toString(), "3/4");
  assert.equal(Rat.from("6/8").toString(), "3/4");
  assert.equal(Rat.from("-1.25").toString(), "-5/4");
  assert.equal(Rat.from(0.1).toString(), "1/10");
  assert.equal(Rat.from("2").toString(), "2");
  assert.equal(Rat.from("1e-3").toString(), "1/1000");
  assert.equal(Rat.from("-4/-6").toString(), "2/3");
});

test("arithmetic is exact", () => {
  assert.equal(Rat.from("1/2").add("1/3").toString(), "5/6");
  assert.equal(Rat.from("1/2").sub("2/3").toString(), "-1/6");
  assert.equal(Rat.from("2/3").mul("9/4").toString(), "3/2");
  assert.equal(Rat.from("2/3").div("4/5").toString(), "5/6");
  assert.equal(Rat.from("2/3").neg().toString(), "-2/3");
  // 0.1 + 0.2 !== 0.3 in floats, but exact here:
  assert.equal(Rat.from(0.1).add(0.2).toString(), "3/10");
});

test("comparison and sign", () => {
  assert.equal(Rat.from("1/3").cmp("2/7"), 1);
  assert.equal(Rat.from("2/6").cmp("1/3"), 0);
  assert.equal(Rat.from("-5/2").sign(), -1);
  assert.equal(Rat.from("0").sign(), 0);
  assert.ok(Rat.from("3/9").equals("1/3"));
});

test("rejects zero denominator and division by zero", () => {
  assert.throws(() => Rat.of(1n, 0n));
  assert.throws(() => Rat.from("1/2").div(0));
});
