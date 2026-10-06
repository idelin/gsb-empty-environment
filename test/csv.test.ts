import { assert, test } from "./harness";
import { parseCsv } from "../src/csv";

test("parseCsv parses header and rows", () => {
  const { header, rows } = parseCsv("name,age\nalice,30\nbob,41\n");
  assert.deepStrictEqual(header, ["name", "age"]);
  assert.strictEqual(rows.length, 2);
  assert.deepStrictEqual(rows[0], { line: 2, record: { name: "alice", age: "30" } });
  assert.deepStrictEqual(rows[1].record, { name: "bob", age: "41" });
});

test("parseCsv handles quotes, escaped quotes, CRLF and missing cells", () => {
  const { rows } = parseCsv('name,note\r\n"a","say ""hi"""\r\nb\r\n');
  assert.strictEqual(rows.length, 2);
  assert.deepStrictEqual(rows[0].record, { name: "a", note: 'say "hi"' });
  assert.deepStrictEqual(rows[1].record, { name: "b", note: "" });
});

test("parseCsv keeps embedded newlines inside quotes", () => {
  const { rows } = parseCsv('name,note\n"a","line1\nline2"\n');
  assert.strictEqual(rows.length, 1);
  assert.strictEqual(rows[0].record.note, "line1\nline2");
});
