import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Pseudonymizer,
  PseudonymizationError,
  REDACTED,
  canonicalize,
  type LogEntry,
  type MappingTable,
} from "../src/index.ts";

const SECRET = "test-secret-key";

function makeEngine(extra: Record<string, unknown> = {}) {
  return new Pseudonymizer({
    secret: SECRET,
    rules: [
      { pattern: "users[].email", action: "pseudonymize", domain: "pii.email" },
      { pattern: "users[].id", action: "pseudonymize", domain: "user.id" },
      { pattern: "orders[].userId", action: "pseudonymize", domain: "user.id" },
      { pattern: "orders[].items[].supplierEmail", action: "pseudonymize", domain: "pii.email" },
      { pattern: "meta.*.token", action: "pseudonymize", domain: "auth.token" },
      { pattern: "users[].country", action: "allow" },
    ],
    ...extra,
  });
}

const sample = {
  users: [
    { id: "u-1", email: "alice@example.com", country: "SE", password: "hunter2" },
    { id: "u-2", email: "bob@example.com", country: "NO", password: "hunter3" },
  ],
  orders: [
    { userId: "u-1", items: [{ supplierEmail: "alice@example.com" }, { supplierEmail: "carol@example.com" }] },
    { userId: "u-2", items: [{ supplierEmail: "bob@example.com" }] },
  ],
  meta: { svcA: { token: "tok-aaa" }, svcB: { token: "tok-bbb" } },
};

test("same domain + same value => same pseudonym (referential consistency)", () => {
  const engine = makeEngine();
  const { data } = engine.pseudonymize(sample) as { data: any };
  const orderEmail = data.orders[0].items[0].supplierEmail;
  // alice@example.com appears as users[0].email (domain pii.email) and orders[0].items[0].supplierEmail (same domain)
  assert.equal(orderEmail, data.users[0].email);
  assert.match(orderEmail, /^psn_[0-9a-f]{24}$/);
});

test("different domains => different pseudonyms for the same value", () => {
  const engine = new Pseudonymizer({
    secret: SECRET,
    rules: [
      { pattern: "a", action: "pseudonymize", domain: "domain.one" },
      { pattern: "b", action: "pseudonymize", domain: "domain.two" },
    ],
  });
  const { data } = engine.pseudonymize({ a: "same-value", b: "same-value" }) as { data: any };
  assert.notEqual(data.a, data.b);
});

test("foreign keys still join across collections via shared domain", () => {
  const engine = makeEngine();
  const { data } = engine.pseudonymize(sample) as { data: any };
  const userIds = new Set(data.users.map((u: any) => u.id));
  for (const order of data.orders) {
    assert.ok(userIds.has(order.userId), `order.userId ${order.userId} must join to a user`);
  }
  assert.notEqual(data.users[0].id, "u-1");
});

test("object key order is insignificant; array order is significant", () => {
  assert.equal(canonicalize({ b: 1, a: { d: [1, 2], c: null } }), canonicalize({ a: { c: null, d: [1, 2] }, b: 1 }));
  assert.notEqual(canonicalize([1, 2]), canonicalize([2, 1]));

  const engine = new Pseudonymizer({
    secret: SECRET,
    rules: [{ pattern: "payload", action: "pseudonymize", domain: "doc" }],
  });
  const r1 = engine.pseudonymize({ payload: { x: 1, y: [true, null] } }) as { data: any };
  const r2 = engine.pseudonymize({ payload: { y: [true, null], x: 1 } }) as { data: any };
  assert.equal(r1.data.payload, r2.data.payload);
});

test("unauthorized fields never pass through (default redact policy)", () => {
  const engine = makeEngine();
  const { data, stats } = engine.pseudonymize(sample) as { data: any; stats: any };
  assert.equal(data.users[0].password, REDACTED);
  assert.equal(data.users[1].password, REDACTED);
  assert.ok(!JSON.stringify(data).includes("hunter2"));
  assert.ok(stats.redacted >= 2);
});

test("error policy reports UNAUTHORIZED_FIELD with concrete path and still redacts", () => {
  const engine = makeEngine({ unauthorizedPolicy: "error" });
  const { data, issues } = engine.pseudonymize(sample) as { data: any; issues: any[] };
  const unauthorized = issues.filter((i) => i.code === "UNAUTHORIZED_FIELD");
  assert.deepEqual(
    unauthorized.map((i) => i.path).sort(),
    ["$.users[0].password", "$.users[1].password"],
  );
  assert.equal(data.users[0].password, REDACTED);
});

test("explicit allow rule passes a field through unchanged", () => {
  const engine = makeEngine();
  const { data } = engine.pseudonymize(sample) as { data: any };
  assert.equal(data.users[0].country, "SE");
});

test("wildcard * matches any single object key", () => {
  const engine = makeEngine();
  const { data } = engine.pseudonymize(sample) as { data: any };
  assert.match(data.meta.svcA.token, /^psn_/);
  assert.match(data.meta.svcB.token, /^psn_/);
  assert.notEqual(data.meta.svcA.token, data.meta.svcB.token);
});

test("nested arrays: every element at every depth is processed", () => {
  const engine = new Pseudonymizer({
    secret: SECRET,
    rules: [{ pattern: "groups[].members[].email", action: "pseudonymize", domain: "pii.email" }],
  });
  const input = {
    groups: [
      { members: [{ email: "a@x.io" }, { email: "b@x.io" }] },
      { members: [{ email: "a@x.io" }] },
    ],
  };
  const { data } = engine.pseudonymize(input) as { data: any };
  assert.match(data.groups[0].members[0].email, /^psn_/);
  assert.equal(data.groups[0].members[0].email, data.groups[1].members[0].email);
  assert.notEqual(data.groups[0].members[0].email, data.groups[0].members[1].email);
});

test("mapping export is separate from redacted data and contains originals + paths", () => {
  const engine = makeEngine();
  const { data } = engine.pseudonymize(sample);
  const mapping = engine.exportMapping();
  const dataJson = JSON.stringify(data);
  assert.ok(!dataJson.includes("alice@example.com"));
  assert.ok(!dataJson.includes("u-1"));
  const mappingJson = JSON.stringify(mapping);
  assert.ok(mappingJson.includes("alice@example.com"), "mapping holds the original for re-identification");
  const alice = mapping.entries.find((e) => e.value === "alice@example.com")!;
  assert.equal(alice.domain, "pii.email");
  assert.ok(alice.paths.includes("$.users[0].email"));
  assert.ok(alice.paths.includes("$.orders[0].items[0].supplierEmail"));
  assert.match(alice.valueHash, /^[0-9a-f]{64}$/);
});

test("mapping reuse keeps pseudonyms stable across runs and engines", () => {
  const engine1 = makeEngine();
  const r1 = engine1.pseudonymize(sample) as { data: any };
  const mapping: MappingTable = engine1.exportMapping();

  const engine2 = makeEngine({ mapping });
  const r2 = engine2.pseudonymize(sample) as { data: any };
  assert.deepEqual(r2.data, r1.data);

  const fresh = makeEngine();
  const r3 = fresh.pseudonymize(sample) as { data: any };
  assert.deepEqual(r3.data, r1.data, "same secret + rules => deterministic output");
});

test("different secret produces different pseudonyms", () => {
  const a = makeEngine();
  const b = makeEngine({ secret: "another-secret" });
  const ra = a.pseudonymize(sample) as { data: any };
  const rb = b.pseudonymize(sample) as { data: any };
  assert.notEqual(ra.data.users[0].email, rb.data.users[0].email);
});

test("collision is detectable and located by path", () => {
  const probe = new Pseudonymizer({
    secret: SECRET,
    rules: [{ pattern: "email", action: "pseudonymize", domain: "pii.email" }],
  });
  const { data: probeData } = probe.pseudonymize({ email: "alice@example.com" }) as { data: any };
  const stolenPseudonym = probeData.email;

  const forged: MappingTable = {
    version: 1,
    entries: [
      { domain: "pii.email", pseudonym: stolenPseudonym, valueHash: "0".repeat(64), value: "mallory@evil.io", paths: ["$.email"] },
    ],
  };
  const engine = new Pseudonymizer({
    secret: SECRET,
    rules: [{ pattern: "email", action: "pseudonymize", domain: "pii.email" }],
    mapping: forged,
  });
  const { data, issues } = engine.pseudonymize({ email: "alice@example.com" }) as { data: any; issues: any[] };
  assert.equal(issues.length, 1);
  assert.equal(issues[0].code, "COLLISION");
  assert.equal(issues[0].path, "$.email");
  assert.equal(data.email, REDACTED, "colliding value must not be emitted under a foreign pseudonym");
});

test("non-JSON values are reported with their path and redacted", () => {
  const engine = new Pseudonymizer({ secret: SECRET, rules: [] });
  const input: any = { a: { b: undefined }, c: NaN, d: () => {} };
  const { data, issues } = engine.pseudonymize(input) as { data: any; issues: any[] };
  assert.deepEqual(
    issues.map((i) => [i.code, i.path]),
    [
      ["NON_JSON_VALUE", "$.a.b"],
      ["NON_JSON_VALUE", "$.c"],
      ["NON_JSON_VALUE", "$.d"],
    ],
  );
  assert.equal(data.a.b, REDACTED);
  assert.equal(data.c, REDACTED);
});

test("cyclic structures are reported, not crashed on", () => {
  const engine = new Pseudonymizer({ secret: SECRET, rules: [] });
  const input: any = { name: "x" };
  input.self = input;
  const { data, issues } = engine.pseudonymize(input) as { data: any; issues: any[] };
  assert.equal(issues[0].code, "NON_JSON_VALUE");
  assert.equal(issues[0].path, "$.self");
  assert.equal(data.self, REDACTED);
});

test("logs never contain original values", () => {
  const entries: LogEntry[] = [];
  const engine = makeEngine({ unauthorizedPolicy: "error", logger: (e) => entries.push(e) });
  engine.pseudonymize(sample);
  assert.ok(entries.length > 0);
  const logJson = JSON.stringify(entries);
  for (const secretValue of ["alice@example.com", "bob@example.com", "hunter2", "hunter3", "u-1", "u-2", "tok-aaa"]) {
    assert.ok(!logJson.includes(secretValue), `log leaked original value: ${secretValue}`);
  }
  assert.ok(logJson.includes("$.users[0].email"), "logs reference paths");
});

test("invalid rules are rejected", () => {
  assert.throws(
    () => new Pseudonymizer({ secret: SECRET, rules: [{ pattern: "a..b", action: "redact" }] }),
    (err: any) => err instanceof PseudonymizationError && err.code === "INVALID_RULE",
  );
  assert.throws(
    () => new Pseudonymizer({ secret: SECRET, rules: [{ pattern: "a", action: "pseudonymize" }] }),
    (err: any) => err instanceof PseudonymizationError && err.code === "INVALID_RULE",
  );
});

test("canonicalize rejects cycles and non-finite numbers with path", () => {
  const cyclic: any = {};
  cyclic.self = cyclic;
  assert.throws(() => canonicalize(cyclic), (e: any) => e.code === "NON_JSON_VALUE" && e.path === "$.self");
  assert.throws(() => canonicalize({ n: Infinity }), (e: any) => e.code === "NON_JSON_VALUE" && e.path === "$.n");
});
