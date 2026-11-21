import test from "node:test";
import assert from "node:assert/strict";

import {
  AuditDecryptionError,
  AuditVault,
  CircularReferenceError,
  Desensitizer,
  assertNoLeak,
  findLeaks,
  restoreWithVault,
} from "../src/index.ts";

const PHONE = "13800138000";
const EMAIL = "alice@example.com";
const TOKEN_PATTERN = /^\{\{[A-Za-z0-9_-]+(?::[^{}]+)?\}\}$/;

function json(value: unknown): Record<string, unknown> {
  return JSON.parse(value as string) as Record<string, unknown>;
}

function assertToken(value: unknown): asserts value is string {
  assert.equal(typeof value, "string");
  if (typeof value !== "string") {
    throw new TypeError("expected a token string");
  }
  assert.match(value, TOKEN_PATTERN);
}

function unicodeEscape(value: string): string {
  return [...value]
    .map(
      (character) =>
        `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`,
    )
    .join("");
}

function htmlEscape(value: string): string {
  return [...value]
    .map((character) => `&#x${character.codePointAt(0)!.toString(16)};`)
    .join("");
}

test("masks fields by sensitivity level while keeping public data", () => {
  const input = {
    id: 42,
    name: "Alice",
    phone: PHONE,
    email: EMAIL,
  };
  const desensitizer = new Desensitizer({
    fields: { phone: "restricted", email: "sensitive" },
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body);

  assert.equal(body.id, 42);
  assert.equal(body.name, "Alice");
  assertToken(body.phone);
  assertToken(body.email);
  assert.equal(result.body.includes(PHONE), false);
  assert.equal(result.body.includes(EMAIL), false);
  assert.equal(JSON.stringify(result).includes(PHONE), false);

  const phoneAudit = result.audit.find((entry) => entry.path === "/phone");
  const emailAudit = result.audit.find((entry) => entry.path === "/email");
  assert.ok(phoneAudit);
  assert.ok(emailAudit);
  assert.equal(phoneAudit.level, "restricted");
  assert.equal(emailAudit.level, "sensitive");
  assert.equal(phoneAudit.reason, "field");
  assert.equal(desensitizer.reveal(phoneAudit.token), PHONE);
  assert.equal(desensitizer.reveal(emailAudit.token), EMAIL);
  assert.equal(typeof desensitizer.serialize(input), "string");

  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("applies path rules to nested objects and array elements", () => {
  const input = {
    users: [
      {
        name: "Alice",
        phone: PHONE,
        email: EMAIL,
        contacts: [{ email: "second@example.com" }],
      },
      {
        name: "Bob",
        phone: "13900139000",
        email: "bob@example.com",
        contacts: [],
      },
    ],
    meta: { phone: PHONE },
  };
  const desensitizer = new Desensitizer({
    fields: {
      "**.phone": "restricted",
      "users[].email": "sensitive",
      "**.contacts[].email": "sensitive",
    },
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;

  assert.equal(body.users[0].name, "Alice");
  assertToken(body.users[0].phone);
  assertToken(body.users[0].email);
  assertToken(body.users[0].contacts[0].email);
  assertToken(body.users[1].phone);
  assertToken(body.users[1].email);
  assertToken(body.meta.phone);
  assertNoLeak(result.body, [
    PHONE,
    EMAIL,
    "second@example.com",
    "13900139000",
    "bob@example.com",
  ]);

  assert.deepEqual(desensitizer.deserialize(result.body), input);
  assert.ok(
    result.audit.some((entry) => entry.path === "/users/0/contacts/0/email"),
  );
});

test("inherits field sensitivity into nested structures and arrays", () => {
  const input = {
    profile: {
      nickname: "Alice",
      tags: ["alpha", "beta"],
      contact: { phone: PHONE, email: EMAIL },
    },
    visible: "public",
  };
  const desensitizer = new Desensitizer({
    fields: { profile: "restricted" },
    nestedMode: "leaves",
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;

  assert.equal(body.visible, "public");
  assert.deepEqual(Object.keys(body.profile).sort(), [
    "contact",
    "nickname",
    "tags",
  ]);
  assertToken(body.profile.nickname);
  assert.equal(body.profile.tags.length, 2);
  assertToken(body.profile.tags[0]);
  assertToken(body.profile.tags[1]);
  assertToken(body.profile.contact.phone);
  assertToken(body.profile.contact.email);
  assert.equal(result.body.includes("Alice"), false);
  assert.equal(result.body.includes(PHONE), false);
  assert.equal(result.body.includes(EMAIL), false);
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("subtree mode replaces an entire nested value with one reversible token", () => {
  const input = {
    profile: {
      tags: ["alpha", "beta"],
      contact: { phone: PHONE },
    },
  };
  const desensitizer = new Desensitizer({
    fields: { profile: "restricted" },
    nestedMode: "subtree",
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body);

  assertToken(body.profile);
  assert.equal(
    result.audit.filter((entry) => entry.reason === "subtree").length,
    1,
  );
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("redacts encoded, separated, and split variants in public content", () => {
  const base64Phone = Buffer.from(PHONE).toString("base64");
  const base64Email = Buffer.from(EMAIL).toString("base64");
  const hexPhone = Buffer.from(PHONE).toString("hex");
  const doubleUrlEmail = encodeURIComponent(encodeURIComponent(EMAIL));
  const base64Parts = [
    base64Phone.slice(0, 8),
    base64Phone.slice(8),
  ];

  const input = {
    phone: PHONE,
    email: EMAIL,
    public: {
      raw: `call ${PHONE} now`,
      url: `mail=${encodeURIComponent(EMAIL)}`,
      doubleUrl: `mail=${doubleUrlEmail}`,
      base64: `value=${base64Phone}`,
      base64Email: `value=${base64Email}`,
      hex: `value=${hexPhone}`,
      unicode: unicodeEscape(PHONE),
      html: htmlEscape(PHONE),
      mixedUnicode: "alice\\u0040example.com",
      mixedHtml: "alice&#64;example.com",
      separated: "138-0013-8000",
      spaced: "138 0013 8000",
      splitArray: ["138", "0013", "8000"],
      splitObject: { a: "138", b: "0013", c: "8000" },
      splitEncoded: base64Parts,
      nested: [{ tenant: "t1", value: `hex:${hexPhone}` }],
    },
  };

  const desensitizer = new Desensitizer({
    fields: { phone: "restricted", email: "sensitive" },
    inferSensitiveFields: false,
  });
  const result = desensitizer.serializeWithAudit(input);

  assertNoLeak(result.body, [PHONE, EMAIL]);
  assert.equal(result.body.includes(PHONE), false);
  assert.equal(result.body.includes(EMAIL), false);
  assert.equal(result.body.includes(base64Phone), false);
  assert.equal(result.body.includes(base64Email), false);
  assert.equal(result.body.includes(hexPhone), false);
  assert.equal(result.body.includes(doubleUrlEmail), false);
  assert.equal(result.body.includes(unicodeEscape(PHONE)), false);
  assert.equal(result.body.includes(htmlEscape(PHONE)), false);
  assert.equal(result.body.includes("alice\\u0040example.com"), false);
  assert.equal(result.body.includes("alice&#64;example.com"), false);
  assert.equal(result.body.includes("138-0013-8000"), false);

  const reasons = new Set(result.audit.map((entry) => entry.reason));
  assert.ok(reasons.has("field"));
  assert.ok(reasons.has("content"));
  assert.ok(reasons.has("split"));
  assert.ok(
    result.audit.some((entry) => entry.encoding === "base64"),
  );
  assert.ok(
    result.audit.some((entry) => entry.encoding?.startsWith("split>")),
  );

  assert.deepEqual(desensitizer.deserialize(result.body), input);
  assert.deepEqual(
    findLeaks(result.body, [PHONE, EMAIL]),
    [],
  );
});

test("detects normalized and case-insensitive variants when enabled", () => {
  const fullWidthPhone = [...PHONE]
    .map((digit) => String.fromCharCode(digit.charCodeAt(0) + 0xfee0))
    .join("");
  const input = {
    secret: "SuperSecret42",
    phone: PHONE,
    noteCase: "the value is supersecret42",
    noteWidth: `dial ${fullWidthPhone}`,
  };
  const desensitizer = new Desensitizer({
    fields: { secret: "restricted", phone: "restricted" },
    inferSensitiveFields: false,
    detection: { caseInsensitive: true, normalized: true },
  });

  const result = desensitizer.serializeWithAudit(input);
  assertNoLeak(result.body, ["SuperSecret42", fullWidthPhone]);
  assert.equal(result.body.toLowerCase().includes("supersecret42"), false);
  assert.equal(result.body.includes(fullWidthPhone), false);
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("does not mutate the input and handles JSON-adjacent values", () => {
  const input = {
    phone: PHONE,
    when: new Date("2026-01-02T03:04:05.000Z"),
    count: 9007199254740993n,
    set: new Set(["a", "b"]),
    map: new Map<string, unknown>([["phone", PHONE]]),
  };
  const snapshot = JSON.stringify(input, (_key, value) =>
    typeof value === "bigint" ? value.toString() : value,
  );
  const desensitizer = new Desensitizer({
    fields: { phone: "restricted" },
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;

  assert.equal(body.phone.includes(PHONE), false);
  assert.equal(body.when, "2026-01-02T03:04:05.000Z");
  assert.equal(body.count, "9007199254740993");
  assert.deepEqual(body.set, ["a", "b"]);
  assert.equal(body.map.phone.includes(PHONE), false);
  assert.equal(
    JSON.stringify(input, (_key, value) =>
      typeof value === "bigint" ? value.toString() : value,
    ),
    snapshot,
  );
});

test("normalizes custom toJSON output before applying field rules", () => {
  const input = {
    value: {
      toJSON() {
        return { phone: PHONE, label: "contact" };
      },
    },
  };
  const desensitizer = new Desensitizer({
    fields: { phone: "restricted" },
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;
  assert.equal(body.value.label, "contact");
  assertToken(body.value.phone);
  assert.equal(result.body.includes(PHONE), false);
});

test("throws a specific error for circular structures", () => {
  const input: Record<string, unknown> = {};
  input.self = input;
  const desensitizer = new Desensitizer();
  assert.throws(
    () => desensitizer.serialize(input),
    CircularReferenceError,
  );
});

test("keeps an encrypted, reversible audit mapping", () => {
  const input = { phone: PHONE, note: `call ${PHONE}` };
  const desensitizer = new Desensitizer({
    fields: { phone: "restricted" },
    inferSensitiveFields: false,
  });
  const result = desensitizer.serializeWithAudit(input);

  const encrypted = result.vault.export("correct horse battery staple");
  assert.equal(encrypted.includes(PHONE), false);
  assert.throws(
    () => AuditVault.import(encrypted, "wrong passphrase"),
    AuditDecryptionError,
  );

  const imported = AuditVault.import(
    encrypted,
    "correct horse battery staple",
  );
  assert.equal(imported.size, result.audit.length);
  assert.deepEqual(
    restoreWithVault(JSON.parse(result.body), imported),
    input,
  );

  const plain = result.vault.export();
  assert.equal(plain.includes(PHONE), true);
  const plainVault = AuditVault.import(plain);
  assert.deepEqual(
    restoreWithVault(JSON.parse(result.body), plainVault),
    input,
  );
});

test("supports per-level replacement strategies", () => {
  const input = {
    internalValue: "internal-value-123",
    sensitiveValue: "sensitive-value-456",
    restrictedValue: "restricted-value-789",
  };
  const desensitizer = new Desensitizer({
    fields: {
      internalValue: "internal",
      sensitiveValue: "sensitive",
      restrictedValue: "restricted",
    },
    inferSensitiveFields: false,
    levelStrategies: {
      internal: "partial",
      sensitive: "tokenize",
      restricted: "hash",
    },
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as Record<string, string>;

  assert.match(body.internalValue, /\*\*\*\*/);
  assert.equal(body.internalValue.includes("internal-value-123"), false);
  assertToken(body.sensitiveValue);
  assertToken(body.restrictedValue);
  assert.match(body.restrictedValue, /\{\{h/);

  const strategies = new Map(
    result.audit.map((entry) => [entry.path, entry.strategy]),
  );
  assert.equal(strategies.get("/internalValue"), "partial");
  assert.equal(strategies.get("/sensitiveValue"), "tokenize");
  assert.equal(strategies.get("/restrictedValue"), "hash");
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("matches field names across case and separators and honours public overrides", () => {
  const input = {
    accessToken: "token-abc-123",
    phone: PHONE,
    displayName: "Alice",
  };
  const desensitizer = new Desensitizer({
    fields: {
      access_token: "restricted",
      phone: "public",
    },
    inferSensitiveFields: true,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body);
  assertToken(body.accessToken);
  assert.equal(body.phone, PHONE);
  assert.equal(body.displayName, "Alice");
});

test("applies a non-public default level to all unconfigured fields", () => {
  const input = {
    a: "value-a",
    nested: { b: "value-b" },
    list: ["value-c"],
  };
  const desensitizer = new Desensitizer({
    defaultLevel: "internal",
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;
  assertToken(body.a);
  assertToken(body.nested.b);
  assertToken(body.list[0]);
  assert.equal(result.body.includes("value-a"), false);
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("scrubs explicitly known sensitive values", () => {
  const known = "CUSTOM-SECRET-42";
  const input = {
    note: `prefix ${known} suffix`,
    exact: known,
  };
  const desensitizer = new Desensitizer({
    knownSensitiveValues: [known],
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  assertNoLeak(result.body, [known]);
  assert.equal(result.body.includes(known), false);
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("redacts sensitive values that appear in object keys", () => {
  const input = {
    phone: PHONE,
    lookup: { [PHONE]: "found" },
  };
  const desensitizer = new Desensitizer({
    fields: { phone: "restricted" },
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;
  const lookupKey = Object.keys(body.lookup)[0];
  assertToken(lookupKey);
  assert.equal(result.body.includes(PHONE), false);
  assert.ok(result.audit.some((entry) => entry.reason === "key"));
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("masks values split across a top-level array", () => {
  const desensitizer = new Desensitizer({
    knownSensitiveValues: [PHONE],
    inferSensitiveFields: false,
  });
  const input = ["138", "0013", "8000"];
  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as unknown as string[];

  assert.equal(body.length, 3);
  for (const item of body) assertToken(item);
  assertNoLeak(result.body, [PHONE]);
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("propagates a field's nested mode and strategy to its descendants", () => {
  const input = {
    profile: {
      name: "alpha-value",
      tags: ["beta-value"],
      contact: { email: EMAIL },
    },
  };
  const desensitizer = new Desensitizer({
    fields: {
      profile: {
        level: "restricted",
        strategy: "hash",
        nestedMode: "leaves",
      },
    },
    nestedMode: "subtree",
    inferSensitiveFields: false,
  });

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as any;

  assert.equal(typeof body.profile, "object");
  assert.match(body.profile.name, /^\{\{h/);
  assert.match(body.profile.tags[0], /^\{\{h/);
  assert.match(body.profile.contact.email, /^\{\{h/);
  for (const entry of result.audit) {
    assert.equal(entry.strategy, "hash");
  }
  assert.deepEqual(desensitizer.deserialize(result.body), input);
});

test("infers compound sensitive names without masking ordinary booleans", () => {
  const input = {
    phone: PHONE,
    phoneEnabled: true,
    accessToken: "token-value-123",
    secretaryName: "Alice",
    userEmail: EMAIL,
  };
  const desensitizer = new Desensitizer();

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body);

  assertToken(body.phone);
  assert.equal(body.phoneEnabled, true);
  assertToken(body.accessToken);
  assert.equal(body.secretaryName, "Alice");
  assertToken(body.userEmail);
  assert.equal(result.body.includes(PHONE), false);
  assert.equal(result.body.includes(EMAIL), false);
});

test("chooses a token delimiter that cannot collide with very short values", () => {
  const desensitizer = new Desensitizer({
    knownSensitiveValues: ["{{"],
    inferSensitiveFields: false,
    detection: { minLength: 2 },
  });
  const input = { note: "a{{b", exact: "{{" };

  const result = desensitizer.serializeWithAudit(input);
  const body = json(result.body) as Record<string, string>;

  assert.equal(result.body.includes("{{"), false);
  assert.match(body.note, /\[\[[A-Za-z0-9_-]+\]\]/);
  assert.match(body.exact, /\[\[[A-Za-z0-9_-]+\]\]/);
  assert.deepEqual(desensitizer.deserialize(result.body), input);

  const defaultDetection = new Desensitizer({
    knownSensitiveValues: ["{{"],
    inferSensitiveFields: false,
  });
  const exactOnly = defaultDetection.serializeWithAudit({ exact: "{{" });
  assert.equal(exactOnly.body.includes("{{"), false);
  assert.deepEqual(defaultDetection.deserialize(exactOnly.body), {
    exact: "{{",
  });
});
