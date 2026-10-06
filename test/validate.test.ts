import { assert, test } from "./harness";
import { parseCsv } from "../src/csv";
import { rules, validateRows } from "../src/validate";

const schema = {
  name: [rules.required()],
  age: [rules.required(), rules.integer()],
  email: [rules.pattern(/^[^@\s]+@[^@\s]+$/)],
};

test("validateRows isolates bad rows without affecting good ones", () => {
  const csv = [
    "name,age,email",
    "alice,30,alice@example.com",
    ",not-a-number,bad-email",
    "bob,41,bob@example.com",
    "",
  ].join("\n");
  const { rows } = parseCsv(csv);
  const result = validateRows(rows, schema);

  assert.deepStrictEqual(
    result.accepted.map((r) => r.record.name),
    ["alice", "bob"]
  );
  assert.strictEqual(result.rejected.length, 1);
  assert.strictEqual(result.rejected[0].line, 3);
  assert.deepStrictEqual(
    result.rejected[0].violations.map((v) => v.field),
    ["name", "age", "email"]
  );
});

test("validateRows reports every violating field per row", () => {
  const { rows } = parseCsv("name,age,email\n,xyz,nope\n");
  const result = validateRows(rows, schema);
  assert.strictEqual(result.accepted.length, 0);
  assert.strictEqual(result.rejected[0].violations.length, 3);
});
