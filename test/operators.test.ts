import { test } from "node:test";
import assert from "node:assert/strict";
import { StringColumn } from "../src/string-column.ts";
import { Bitmap } from "../src/bitmap.ts";
import { startsWith, equals, like, matchRegex } from "../src/operators.ts";
import { generateUrls } from "../examples/urls.ts";

test("StringColumn stores rows in a compact buffer with offsets", () => {
  const column = StringColumn.from(["abc", "", "de", "🚀"]);
  assert.equal(column.length, 4);
  assert.equal(column.offsets.length, 5);
  assert.equal(column.get(0), "abc");
  assert.equal(column.get(1), "");
  assert.equal(column.get(2), "de");
  assert.equal(column.get(3), "🚀");
  // No slack: buffer size is exactly the sum of UTF-8 byte lengths.
  assert.equal(column.bytes.byteLength, 3 + 0 + 2 + 4);
});

test("Bitmap packs 1 bit per row and iterates set rows", () => {
  const bitmap = new Bitmap(70);
  bitmap.set(0);
  bitmap.set(31);
  bitmap.set(32);
  bitmap.set(69);
  assert.equal(bitmap.byteLength, 12); // ceil(70/32) words * 4 bytes
  assert.equal(bitmap.cardinality(), 4);
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 31, 32, 69]);
  assert.ok(bitmap.get(69));
  assert.ok(!bitmap.get(68));
});

test("startsWith matches prefix bytes without decoding strings", () => {
  const column = StringColumn.from([
    "https://api.example.com/v1",
    "https://www.example.com",
    "http://api.example.com",
    "https://api.", // exactly the prefix
    "https://ap", // shorter than the prefix
    "",
  ]);
  const bitmap = startsWith(column, "https://api.");
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 3]);
});

test("startsWith handles UTF-8 prefixes at the byte level", () => {
  const column = StringColumn.from(["🚀launch", "🚀", "🛰️sat", "rocket🚀"]);
  const bitmap = startsWith(column, "🚀");
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 1]);
});

test("equals matches whole values only", () => {
  const column = StringColumn.from(["a", "ab", "a", "", "ba"]);
  const bitmap = equals(column, "a");
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 2]);
});

test("like uses the prefix fast path for 'literal%'", () => {
  const column = StringColumn.from(["https://api.a.com", "https://api.", "https://apx", "xhttps://api."]);
  const bitmap = like(column, "https://api.%");
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 1]);
});

test("like compiles general wildcards to regex", () => {
  const column = StringColumn.from([
    "https://api.example.com/v1/users",
    "https://www.example.com/v1/users",
    "https://api.example.com",
    "ftp://api.example.com/v1/users",
  ]);
  const bitmap = like(column, "https://%.com/v1/_sers");
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 1]);
});

test("like without wildcards is exact match", () => {
  const column = StringColumn.from(["abc", "abcd", "abc"]);
  assert.deepEqual(Array.from(like(column, "abc").toIndices()), [0, 2]);
});

test("matchRegex filters with a user-supplied pattern", () => {
  const column = StringColumn.from(["user_1", "admin", "user_2", "guest"]);
  const bitmap = matchRegex(column, /^user_\d+$/);
  assert.deepEqual(Array.from(bitmap.toIndices()), [0, 2]);
});

test("acceptance: 100k URL rows, url LIKE 'https://api.%'", () => {
    const rows = generateUrls(100_000);
  const column = StringColumn.from(rows);

  const t0 = performance.now();
  const bitmap = like(column, "https://api.%");
  const elapsedMs = performance.now() - t0;

  // Ground truth from the plain JS semantics.
  const expected = rows.map((r) => r.startsWith("https://api."));
  assert.equal(bitmap.cardinality(), expected.filter(Boolean).length);
  for (let i = 0; i < rows.length; i++) {
    assert.equal(bitmap.get(i), expected[i], `row ${i}`);
  }

  // Compressed bitmap: 1 bit per row.
  assert.equal(bitmap.byteLength, Math.ceil(rows.length / 32) * 4);

  // Vectorized byte scan must be effectively instant for 100k rows.
  assert.ok(elapsedMs < 100, `filter took ${elapsedMs.toFixed(2)} ms`);
  console.log(
    `    100k-row LIKE 'https://api.%': ${elapsedMs.toFixed(2)} ms, ` +
      `${bitmap.cardinality().toLocaleString()} matches, bitmap ${bitmap.byteLength} bytes`,
  );
});
