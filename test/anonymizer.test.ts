import { test } from "node:test";
import assert from "node:assert/strict";
import { anonymize } from "../src/index.ts";
import type { AnonymizeOptions, JsonValue } from "../src/index.ts";

const KEY = "test-secret-key";

const baseRules = [
  { path: "users.*.id", domain: "user-id" },
  { path: "users.*.email", domain: "email" },
  { path: "users.*.contacts.*.email", domain: "email" },
  { path: "orders.*.userId", domain: "user-id" },
  { path: "orders.*.id", domain: "order-id" },
];

function run(input: JsonValue, extra: Partial<AnonymizeOptions> = {}) {
  return anonymize(input, { rules: baseRules, key: KEY, ...extra });
}

const sample = {
  users: [
    { id: "u-1", email: "alice@example.com", contacts: [{ email: "alice@example.com" }] },
    { id: "u-2", email: "bob@example.com", contacts: [{ email: "carol@example.com" }] },
  ],
  orders: [
    { id: "o-1", userId: "u-1" },
    { id: "o-2", userId: "u-2" },
    { id: "o-3", userId: "u-1" },
  ],
};

test("same domain + same value yields the same pseudonym (stable across runs)", () => {
  const first = run(sample);
  const second = run(sample);
  assert.deepEqual(first.data, second.data);
  const users = (first.data as any).users;
  assert.equal(users[0].contacts[0].email, users[0].email);
});

test("different domains yield different pseudonyms for the same value", () => {
  const result = run(
    { a: "shared", b: "shared" },
    {
      rules: [
        { path: "a", domain: "domain-a" },
        { path: "b", domain: "domain-b" },
      ],
    },
  );
  const data = result.data as any;
  assert.notEqual(data.a, data.b);
  assert.match(data.a, /^ps_[0-9a-f]{24}$/);
});

test("foreign keys still join: user.id and order.userId share a domain", () => {
  const result = run(sample);
  const data = result.data as any;
  const userIds = new Set(data.users.map((u: any) => u.id));
  for (const order of data.orders) {
    assert.ok(userIds.has(order.userId), `order ${order.id} keeps referential link`);
  }
});

test("object key order in input does not change pseudonyms", () => {
  const rules = [{ path: "profile", domain: "profile" }];
  const a = anonymize(
    { profile: { name: "n", tags: ["x", "y"], nested: { p: 1, q: 2 } } },
    { rules, key: KEY },
  );
  const b = anonymize(
    { profile: { nested: { q: 2, p: 1 }, tags: ["x", "y"], name: "n" } },
    { rules, key: KEY },
  );
  assert.equal((a.data as any).profile, (b.data as any).profile);
});

test("array order is significant for pseudonyms", () => {
  const rules = [{ path: "v", domain: "d" }];
  const a = anonymize({ v: [1, 2] }, { rules, key: KEY });
  const b = anonymize({ v: [2, 1] }, { rules, key: KEY });
  assert.notEqual((a.data as any).v, (b.data as any).v);
});

test("nested sensitive fields inside arrays are all processed", () => {
  const result = run(sample);
  const raw = JSON.stringify(result.data);
  assert.ok(!raw.includes("alice@example.com"));
  assert.ok(!raw.includes("bob@example.com"));
  assert.ok(!raw.includes("carol@example.com"));
  assert.ok(!raw.includes("u-1"));
  const contacts = (result.data as any).users[0].contacts;
  assert.match(contacts[0].email, /^ps_/);
});

test("unauthorized fields are redacted, never passed through, and located by path", () => {
  const result = run({ users: [{ id: "u-1", email: "a@b.c", ssn: "123-45-6789" }] });
  const data = result.data as any;
  assert.equal(data.users[0].ssn, "[REDACTED]");
  assert.ok(!JSON.stringify(result.data).includes("123-45-6789"));
  const err = result.errors.find((e) => e.code === "UNAUTHORIZED_FIELD");
  assert.ok(err);
  assert.equal(err.path, "$.users[0].ssn");
  assert.ok(!err.message.includes("123-45-6789"));
});

test("mapping export is separate from data and contains no raw values", () => {
  const result = run(sample);
  const mappingJson = JSON.stringify(result.mapping);
  for (const secret of ["alice@example.com", "bob@example.com", "carol@example.com", "u-1", "u-2", "o-1"]) {
    assert.ok(!mappingJson.includes(secret), `mapping leaks ${secret}`);
    assert.ok(!JSON.stringify(result.errors).includes(secret), `errors leak ${secret}`);
  }
  const emailDomain = result.mapping.domains.find((d) => d.domain === "email");
  assert.ok(emailDomain);
  assert.equal(emailDomain.entries.length, 3);
  const alice = emailDomain.entries.find((e) =>
    e.occurrences.includes("$.users[0].email"),
  );
  assert.ok(alice);
  assert.deepEqual(alice.occurrences, ["$.users[0].contacts[0].email", "$.users[0].email"]);
});

test("collisions are detectable and located by path", () => {
  const values = Array.from({ length: 1000 }, (_, i) => `value-${i}`);
  const result = anonymize(
    { items: values },
    {
      rules: [{ path: "items.*", domain: "d" }],
      key: KEY,
      pseudonymLength: 4,
    },
  );
  assert.ok(result.stats.collisions > 0, "expected collisions with 4-hex-char pseudonyms");
  const collision = result.errors.find((e) => e.code === "COLLISION");
  assert.ok(collision);
  assert.match(collision.path, /^\$\.items\[\d+\]$/);
  assert.equal(result.mapping.collisions.length, result.stats.collisions);
});

test("redact and drop actions", () => {
  const result = run(
    { users: [{ id: "u-1", email: "a@b.c", note: "x", tmp: "y" }] },
    {
      rules: [
        ...baseRules,
        { path: "users.*.note", domain: "n", action: "redact" },
        { path: "users.*.tmp", domain: "n", action: "drop" },
      ],
    },
  );
  const user = (result.data as any).users[0];
  assert.equal(user.note, "[REDACTED]");
  assert.ok(!("tmp" in user));
});

test("root-level scalar rule and $ path", () => {
  const result = anonymize("secret", { rules: [{ path: "$", domain: "root" }], key: KEY });
  assert.match(result.data as string, /^ps_/);
  assert.equal(result.errors.length, 0);
});

test("invalid rule path is reported without aborting", () => {
  const result = run({ users: [] }, {
    rules: [...baseRules, { path: "a..b", domain: "bad" }],
  });
  assert.ok(result.errors.some((e) => e.code === "INVALID_RULE"));
});

test("error messages and logs never contain original values", () => {
  const result = run({
    users: [{ id: "u-1", email: "alice@example.com", password: "hunter2" }],
  });
  const everything = JSON.stringify(result.errors) + JSON.stringify(result.mapping);
  assert.ok(!everything.includes("hunter2"));
  assert.ok(!everything.includes("alice@example.com"));
});
