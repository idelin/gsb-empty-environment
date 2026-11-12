import { test } from "node:test";
import assert from "node:assert/strict";
import { parseStringDictionary } from "../src/structured-fields.ts";

test("parses a dictionary of strings", () => {
  const parsed = parseStringDictionary(
    'default="https://example.com/reports", csp="https://csp.example.com/r"',
  );
  assert.equal(parsed?.get("default"), "https://example.com/reports");
  assert.equal(parsed?.get("csp"), "https://csp.example.com/r");
});

test("accepts tabs and irregular whitespace around members", () => {
  const parsed = parseStringDictionary('a="1" ,\t b="2"');
  assert.equal(parsed?.get("a"), "1");
  assert.equal(parsed?.get("b"), "2");
});

test("handles escaped quotes and backslashes in strings", () => {
  const parsed = parseStringDictionary('a="x\\"y\\\\z"');
  assert.equal(parsed?.get("a"), 'x"y\\z');
});

test("duplicate keys keep the last value", () => {
  const parsed = parseStringDictionary('a="1", a="2"');
  assert.equal(parsed?.get("a"), "2");
});

test("rejects a bare key (boolean true is not a string)", () => {
  assert.equal(parseStringDictionary("default"), null);
});

test("rejects non-string member values", () => {
  assert.equal(parseStringDictionary("a=123"), null);
  assert.equal(parseStringDictionary("a=1.5"), null);
  assert.equal(parseStringDictionary("a=?1"), null);
  assert.equal(parseStringDictionary("a=@1659578233"), null);
  assert.equal(parseStringDictionary("a=%c3%a9"), null);
  assert.equal(parseStringDictionary("a=token123"), null);
  assert.equal(parseStringDictionary("a=:aGVsbG8=:"), null);
});

test("rejects inner lists and parameters", () => {
  assert.equal(parseStringDictionary('a=("x" "y")'), null);
  assert.equal(parseStringDictionary('a="x";p=1'), null);
});

test("one bad member invalidates the whole header", () => {
  assert.equal(parseStringDictionary('good="https://ok.example/", bad=1'), null);
  assert.equal(parseStringDictionary('bad=1, good="https://ok.example/"'), null);
});

test("rejects malformed syntax", () => {
  assert.equal(parseStringDictionary(""), null);
  assert.equal(parseStringDictionary('a="unterminated'), null);
  assert.equal(parseStringDictionary('a="x",'), null);
  assert.equal(parseStringDictionary(',a="x"'), null);
  assert.equal(parseStringDictionary('a="x" b="y"'), null);
  assert.equal(parseStringDictionary('A="x"'), null); // keys are lowercase
  assert.equal(parseStringDictionary('a="bad\\esc"'), null);
});
