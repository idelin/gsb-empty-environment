import { test } from "node:test";
import assert from "node:assert/strict";
import { parseDictionary, parseDictionaryHeaders } from "../src/structured-dict.ts";

test("parses string members", () => {
  const dict = parseDictionary('default="https://a.example/reports", other="https://b.example/"');
  assert.ok(dict);
  assert.deepEqual(dict.get("default")?.value, {
    kind: "string",
    value: "https://a.example/reports",
  });
  assert.deepEqual(dict.get("other")?.value, { kind: "string", value: "https://b.example/" });
});

test("multiple header fields are combined; first duplicate key wins", () => {
  const dict = parseDictionaryHeaders([
    'default="https://one.example/"',
    'default="https://two.example/", other="https://b.example/"',
  ]);
  assert.ok(dict);
  assert.deepEqual(dict.get("default")?.value, {
    kind: "string",
    value: "https://one.example/",
  });
  assert.deepEqual(dict.get("other")?.value, { kind: "string", value: "https://b.example/" });
});

test("duplicate parameters on a member: first occurrence wins", () => {
  const dict = parseDictionary('a="x";priority=1;priority=2;flag;flag=?0');
  assert.ok(dict);
  const member = dict.get("a");
  assert.deepEqual(member?.params.get("priority"), { kind: "number", value: 1 });
  assert.deepEqual(member?.params.get("flag"), { kind: "boolean", value: true });
});

test("member without value defaults to boolean true", () => {
  const dict = parseDictionary("a, b=?0");
  assert.ok(dict);
  assert.deepEqual(dict.get("a")?.value, { kind: "boolean", value: true });
  assert.deepEqual(dict.get("b")?.value, { kind: "boolean", value: false });
});

test("parses tokens, numbers and byte sequences", () => {
  const dict = parseDictionary("t=abc/def:x, n=-42.5, b=:aGVsbG8=:");
  assert.ok(dict);
  assert.deepEqual(dict.get("t")?.value, { kind: "token", value: "abc/def:x" });
  assert.deepEqual(dict.get("n")?.value, { kind: "number", value: -42.5 });
  const bytes = dict.get("b")?.value;
  assert.equal(bytes?.kind, "bytes");
});

test("any syntax error fails the whole header", () => {
  assert.equal(parseDictionary('ok="yes", !!!bad'), null);
  assert.equal(parseDictionary('a="unterminated'), null);
  assert.equal(parseDictionary('a="x",'), null); // trailing comma
  assert.equal(parseDictionary('a="x" y="z"'), null); // missing comma
  assert.equal(parseDictionary("1a=2"), null); // bad key
  assert.equal(parseDictionary('a="x";1bad=2'), null); // bad param key
});

test("empty input yields an empty dictionary", () => {
  const dict = parseDictionary("");
  assert.ok(dict);
  assert.equal(dict.size, 0);
});
