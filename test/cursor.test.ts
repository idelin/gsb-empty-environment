import { test } from "node:test";
import assert from "node:assert/strict";
import { expand, ExpandError, type ExpandInput } from "../src/index.ts";

const input: ExpandInput = {
  template: { pair: [{ $param: "a" }, { $param: "b" }] },
  templateVersion: "t1",
  axes: [
    { name: "a", domain: [1, 2], version: "va" },
    { name: "b", domain: ["x", "y", "z"], version: "vb" },
  ],
};

test("budget interruption returns a resumable cursor and no fake total", () => {
  const first = expand(input, { budget: 4 });
  assert.equal(first.records.length, 4);
  assert.equal(first.complete, false);
  assert.equal(first.total, null);
  assert.ok(first.cursor);

  const rest = expand(input, { cursor: first.cursor! });
  assert.equal(rest.complete, true);
  assert.equal(rest.total, 6);
  assert.deepEqual(
    [...first.records, ...rest.records],
    expand(input).records,
  );
});

test("chained cursors page through the whole expansion in order", () => {
  const pages: unknown[] = [];
  let cursor: string | undefined;
  for (;;) {
    const page = expand(input, { budget: 2, cursor });
    pages.push(...page.records);
    if (page.complete) break;
    cursor = page.cursor!;
  }
  assert.deepEqual(pages, expand(input).records);
});

test("cursor is bound to the input digest: parameter change cannot resume", () => {
  const { cursor } = expand(input, { budget: 2 });
  const changed = {
    ...input,
    axes: [
      { name: "a", domain: [1, 2], version: "va" },
      { name: "b", domain: ["x", "y", "w"], version: "vb" }, // "z" -> "w"
    ],
  };
  assert.throws(() => expand(changed, { cursor: cursor! }), (err) => {
    assert.ok(err instanceof ExpandError);
    assert.equal(err.source, "cursor");
    return true;
  });
});

test("cursor is bound to versions: version bump alone invalidates it", () => {
  const { cursor } = expand(input, { budget: 2 });
  const bumped = {
    ...input,
    axes: [
      { name: "a", domain: [1, 2], version: "va2" },
      { name: "b", domain: ["x", "y", "z"], version: "vb" },
    ],
  };
  assert.throws(() => expand(bumped, { cursor: cursor! }), ExpandError);
});

test("cursor is bound to the template", () => {
  const { cursor } = expand(input, { budget: 2 });
  const other = { ...input, template: { solo: { $param: "a" } } };
  assert.throws(() => expand(other, { cursor: cursor! }), ExpandError);
});

test("malformed cursors are rejected with a cursor-source error", () => {
  assert.throws(
    () => expand(input, { cursor: "not-a-cursor" }),
    (err) => err instanceof ExpandError && err.source === "cursor",
  );
});

test("budget larger than the total completes without a cursor", () => {
  const result = expand(input, { budget: 100 });
  assert.equal(result.complete, true);
  assert.equal(result.total, 6);
  assert.equal(result.cursor, null);
});
