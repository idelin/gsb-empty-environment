import { test } from "node:test";
import assert from "node:assert/strict";
import { formatPath, parsePath } from "../src/path.ts";

test("parsePath parses dotted object paths", () => {
  assert.deepEqual(parsePath("user.address.city"), ["user", "address", "city"]);
});

test("parsePath parses mixed array index paths", () => {
  assert.deepEqual(parsePath("items[0].price"), ["items", 0, "price"]);
  assert.deepEqual(parsePath("a[12][3].b"), ["a", 12, 3, "b"]);
});

test("formatPath round-trips with parsePath", () => {
  for (const path of ["user.address.city", "items[0].price", "x", "m[4]"]) {
    assert.equal(formatPath(parsePath(path)), path);
  }
});

test("parsePath rejects invalid paths", () => {
  assert.throws(() => parsePath(""), /Empty path/);
  assert.throws(() => parsePath("a[1"), /missing/);
  assert.throws(() => parsePath("a[x]"), /Invalid array index/);
});
