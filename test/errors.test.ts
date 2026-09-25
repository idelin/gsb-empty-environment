import { test } from "node:test";
import assert from "node:assert/strict";
import { expand, ExpandError, type ExpandInput } from "../src/index.ts";

function expectExpandError(
  input: ExpandInput,
  source: string,
  path: string,
): void {
  try {
    expand(input);
  } catch (err) {
    assert.ok(err instanceof ExpandError, `expected ExpandError, got ${err}`);
    assert.equal(err.source, source);
    assert.equal(err.path, path);
    return;
  }
  assert.fail(`expected ExpandError at ${path}`);
}

test("unknown $param in template is located to its path", () => {
  expectExpandError(
    {
      template: { items: [{ ok: { $param: "a" } }, { bad: { $param: "nope" } }] },
      axes: [{ name: "a", domain: [1] }],
    },
    "template",
    "$.template.items[1].bad.$param",
  );
});

test("$param node with sibling keys is rejected at its path", () => {
  expectExpandError(
    {
      template: { wrap: { $param: "a", extra: 1 } },
      axes: [{ name: "a", domain: [1] }],
    },
    "template",
    "$.template.wrap",
  );
});

test("non-JSON domain value is located in the axes tree", () => {
  expectExpandError(
    {
      template: { v: { $param: "a" } },
      axes: [{ name: "a", domain: [1, Number.NaN] }],
    },
    "params",
    "$.axes[0].domain[1]",
  );
});

test("duplicate axis names are rejected", () => {
  expectExpandError(
    {
      template: { v: { $param: "a" } },
      axes: [
        { name: "a", domain: [1] },
        { name: "a", domain: [2] },
      ],
    },
    "params",
    "$.axes[1].name",
  );
});

test("constraint referencing an unknown parameter is located", () => {
  expectExpandError(
    {
      template: { v: { $param: "a" } },
      axes: [{ name: "a", domain: [1] }],
      constraints: [
        {
          kind: "implies",
          if: { param: "ghost", equals: 1 },
          then: { param: "a", equals: 1 },
        },
      ],
    },
    "constraints",
    "$.constraints[0].if.param",
  );
});

test("identity path missing from records raises an identity error", () => {
  try {
    expand({
      template: { v: { $param: "a" } },
      axes: [{ name: "a", domain: [1] }],
      identity: ["$.missing.key"],
    });
    assert.fail("expected ExpandError");
  } catch (err) {
    assert.ok(err instanceof ExpandError);
    assert.equal(err.source, "identity");
    assert.equal(err.path, "$.missing.key");
  }
});

test("error message embeds source and path for logging", () => {
  try {
    expand({
      template: { v: { $param: "zzz" } },
      axes: [{ name: "a", domain: [1] }],
    });
    assert.fail("expected ExpandError");
  } catch (err) {
    const msg = (err as Error).message;
    assert.match(msg, /source=template/);
    assert.match(msg, /path=\$\.template\.v\.\$param/);
  }
});
