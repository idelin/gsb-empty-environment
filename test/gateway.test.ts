import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import {
  admitNormalized,
  admitRequest,
  canonicalizeNumber,
  createAdmissionMiddleware,
  createRequestGate,
  evaluateWaf,
  normalizeRequest,
  normalizedBody,
  routeRequest,
  sha256Hex,
  signNormalized,
  verifyNormalizedSignature,
} from "../index.ts";

function bytesOf(value: string): Buffer {
  return Buffer.from(value, "utf8");
}

function rangeBytes(body: string, range: { start: number; end: number }): string {
  return bytesOf(body).subarray(range.start, range.end).toString("utf8");
}

test("legal single-value request is allowed identically by WAF and router", () => {
  const body =
    '{"role":"user","tenantId":"tenant-1","scope":"orders:read","id":42,"theme":"dark"}';
  const decision = admitRequest(body);

  assert.equal(decision.allow, true);
  assert.equal(decision.consistent, true);
  assert.equal(decision.waf.allow, true);
  assert.equal(decision.router.allow, true);
  assert.strictEqual(decision.waf.normalized, decision.router.normalized);
  assert.strictEqual(
    decision.waf.normalized,
    decision.normalized,
  );
  assert.equal(decision.normalized?.auth.role, "user");
  assert.equal(decision.normalized?.auth.tenantId, "tenant-1");
  assert.equal(decision.normalized?.auth.scope, "orders:read");
  assert.equal(decision.normalized?.document.value.id, 42);
  assert.equal(decision.normalized?.duplicates.length, 0);
  assert.equal(
    rangeBytes(
      body,
      decision.normalized!.document.root as { start: number; end: number },
    ),
    body,
  );
});

test("first and last values of a protected key can never split the decision", () => {
  const body = '{"role":"user","tenantId":"t1","scope":"read","role":"admin"}';
  const decision = admitRequest(body);

  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.waf.allow, false);
  assert.equal(decision.router.allow, false);
  assert.ok(
    decision.reasons.some((reason) => reason.code === "PROTECTED_KEY_DUPLICATE"),
  );

  const duplicate = decision.normalized?.duplicates.find(
    (item) => item.key === "role",
  );
  assert.ok(duplicate);
  assert.equal(duplicate.protected, true);
  assert.equal(duplicate.resolution, "reject");
  assert.deepEqual(
    duplicate.occurrences.map((occurrence) => occurrence.selected),
    [false, true],
  );
  assert.deepEqual(
    duplicate.occurrences.map((occurrence) =>
      rangeBytes(body, occurrence.valueRange),
    ),
    ['"user"', '"admin"'],
  );

  assert.equal(decision.waf.auth.role, "admin");
  assert.equal(decision.router.auth.role, "admin");
});

test("nested protected duplicates are rejected and non-auth duplicates fold", () => {
  const protectedBody =
    '{"profile":{"role":"user","role":"admin"},"trace":"ok"}';
  const denied = admitRequest(protectedBody);
  assert.equal(denied.allow, false);
  assert.equal(denied.consistent, true);
  assert.equal(denied.normalized?.duplicates[0].path, "$.profile.role");
  assert.ok(
    denied.reasons.some(
      (reason) =>
        reason.code === "PROTECTED_KEY_DUPLICATE" &&
        reason.path === "$.profile.role",
    ),
  );

  const nestedBody = '{"profile":{"name":"first","name":"last"}}';
  const last = admitRequest(nestedBody);
  assert.equal(last.allow, true);
  assert.equal(last.consistent, true);
  assert.deepEqual(last.normalized?.document.value.profile, { name: "last" });
  assert.equal(last.normalized?.duplicates[0].path, "$.profile.name");

  const first = admitRequest(nestedBody, {}, { duplicatePolicy: "first" });
  assert.equal(first.allow, true);
  assert.deepEqual(first.normalized?.document.value.profile, { name: "first" });
  assert.notEqual(first.normalized?.digest, last.normalized?.digest);
});

test("escaped spellings of the same key are decoded before duplicate checks", () => {
  const body = String.raw`{"role":"user","r\u006fle":"admin"}`;
  const decision = admitRequest(body);

  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.normalized?.duplicates.length, 1);
  assert.deepEqual(decision.normalized?.duplicates[0].keys, ["role"]);
  assert.equal(decision.normalized?.duplicates[0].path, "$.role");
  assert.deepEqual(
    decision.normalized?.duplicates[0].occurrences.map((occurrence) =>
      rangeBytes(body, occurrence.valueRange),
    ),
    ['"user"', '"admin"'],
  );

  const singleEscaped = admitRequest(String.raw`{"r\u006fle":"user"}`);
  assert.equal(singleEscaped.allow, true);
  assert.equal(singleEscaped.normalized?.auth.role, "user");
});

test("case-only auth key variants cannot bypass protected-key checks", () => {
  const collision = admitRequest('{"role":"user","ROLE":"admin"}');
  assert.equal(collision.allow, false);
  assert.equal(collision.consistent, true);
  assert.equal(collision.normalized?.duplicates[0].caseVariants, true);
  assert.deepEqual(collision.normalized?.duplicates[0].keys, [
    "role",
    "ROLE",
  ]);

  const singletonVariant = admitRequest('{"Role":"admin"}');
  assert.equal(singletonVariant.allow, false);
  assert.equal(singletonVariant.consistent, true);
  assert.ok(
    singletonVariant.reasons.some(
      (reason) => reason.code === "AUTH_KEY_CASE_MISMATCH",
    ),
  );
});

test("excessive nesting is denied consistently without stack overflow", () => {
  const tooDeep = `${"[".repeat(1000)}0${"]".repeat(1000)}`;
  const decision = admitRequest(tooDeep);
  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.normalized, null);
  assert.equal(decision.reasons[0].code, "DEPTH_EXCEEDED");

  const exactlyAtLimit = `${"[".repeat(64)}0${"]".repeat(64)}`;
  const accepted = admitRequest(exactlyAtLimit);
  assert.equal(accepted.allow, true);
  assert.equal(accepted.normalized?.stats.maxDepthSeen, 64);

  const onePastLimit = `${"[".repeat(65)}0${"]".repeat(65)}`;
  assertDenied(admitRequest(onePastLimit), "DEPTH_EXCEEDED");
});

function assertDenied(
  decision: ReturnType<typeof admitRequest>,
  code: string,
): void {
  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.reasons[0].code, code);
}

test("number tokens retain exact precision and canonicalize equivalent forms", () => {
  const body =
    '{"id":9007199254740993,"huge":1e400,"thirty":1e30,"hundred":100,"same":1e2,"fraction":1.2300}';
  const normalized = normalizeRequest(body);
  const value = normalized.document.value as Record<string, unknown>;

  assert.equal(value.id, "9007199254740993");
  assert.equal(value.huge, "1e400");
  assert.equal(value.thirty, "1e30");
  assert.equal(value.hundred, 100);
  assert.equal(value.same, 100);
  assert.equal(value.fraction, 1.23);
  assert.match(normalized.canonicalJson, /9\.007199254740993e15/);
  assert.match(normalized.canonicalJson, /1e400/);
  assert.equal(canonicalizeNumber("100"), "1e2");
  assert.equal(canonicalizeNumber("1e2"), "1e2");
  assert.equal(canonicalizeNumber("1.2300"), "1.23");

  assert.equal(
    normalizeRequest('{"n":100}').digest,
    normalizeRequest('{"n":1e2}').digest,
  );
});

test("UTF-8 byte ranges use bytes, not UTF-16 indexes", () => {
  const body = '{"😀":"x","role":"user","r\\u006fle2":"ok"}';
  const normalized = normalizeRequest(body);
  const roleEntry = normalized.root.entries.find(
    (entry) => entry.key === "role",
  );
  assert.ok(roleEntry);

  const jsIndex = body.indexOf('"role"');
  assert.notEqual(jsIndex, roleEntry.keyRange.start);
  assert.equal(rangeBytes(body, roleEntry.keyRange), '"role"');
  assert.equal(rangeBytes(body, roleEntry.value), '"user"');
  assert.equal(normalized.document.root.end, bytesOf(body).length);
});

test("literal and escaped surrogate pairs compare as the same key", () => {
  const body = String.raw`{"😀":"first","\uD83D\uDE00":"last"}`;
  const normalized = normalizeRequest(body);

  assert.equal(normalized.duplicates.length, 1);
  assert.deepEqual(normalized.duplicates[0].keys, ["😀"]);
  assert.equal(normalized.document.value["😀"], "last");
  assert.deepEqual(
    normalized.duplicates[0].occurrences.map((occurrence) =>
      rangeBytes(body, occurrence.valueRange),
    ),
    ['"first"', '"last"'],
  );

  assert.equal(
    admitRequest(String.raw`{"key":"\uD800"}`).reasons[0].code,
    "INVALID_SURROGATE",
  );
});

test("signature covers the collapsed canonical document, not the raw body", () => {
  const lastBody = '{"trace":"old","trace":"new","n":1}';
  const last = normalizeRequest(lastBody, { duplicatePolicy: "last" });
  const lastSignature = signNormalized(last);

  assert.equal(
    last.digest,
    createHash("sha256").update(last.canonicalJson).digest("hex"),
  );
  assert.equal(sha256Hex(last.canonicalJson), last.digest);
  assert.equal(
    admitRequest(lastBody, {
      "x-json-signature": `sha256=${lastSignature}`,
    }).allow,
    true,
  );

  const rawSignature = createHash("sha256").update(lastBody).digest("hex");
  const rawSigned = admitRequest(lastBody, {
    "x-json-signature": rawSignature,
  });
  assert.equal(rawSigned.allow, false);
  assert.ok(
    rawSigned.reasons.some((reason) => reason.code === "SIGNATURE_INVALID"),
  );

  const first = normalizeRequest(lastBody, { duplicatePolicy: "first" });
  const firstSignature = signNormalized(first, "shared-secret");
  assert.equal(
    admitRequest(
      lastBody,
      { "x-json-signature": `hmac-sha256=${firstSignature}` },
      { duplicatePolicy: "first", signature: { secret: "shared-secret" } },
    ).allow,
    true,
  );
  assert.equal(
    admitRequest(
      lastBody,
      { "x-json-signature": lastSignature },
      { duplicatePolicy: "first" },
    ).allow,
    false,
  );

  assert.equal(verifyNormalizedSignature(last, lastSignature), true);
  assert.equal(verifyNormalizedSignature(last, "0".repeat(64)), false);
});

test("signature can be required and whitespace/reordering remains canonical", () => {
  const normalized = normalizeRequest('{ "n" : 1 }');
  const signature = signNormalized(normalized);
  assert.equal(
    admitRequest('{"n":1}', { "x-json-signature": signature }).allow,
    true,
  );
  assert.equal(
    admitRequest('{"n":1}', {}, { signature: { required: true } }).allow,
    false,
  );
});

test("duplicate policy reject denies non-auth duplicates in both layers", () => {
  const decision = admitRequest(
    '{"trace":"one","trace":"two"}',
    {},
    { duplicatePolicy: "reject" },
  );
  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.waf.allow, false);
  assert.equal(decision.router.allow, false);
  assert.equal(decision.reasons[0].code, "DUPLICATE_KEY_REJECTED");
});

test("parse failures produce the same deny decision in both layers", () => {
  const decision = admitRequest('{"a":}');
  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.equal(decision.normalized, null);
  assert.equal(decision.waf.normalized, null);
  assert.equal(decision.router.normalized, null);
  assert.equal(decision.reasons[0].code, "UNEXPECTED_TOKEN");
});

test("downstream APIs accept only the immutable normalized object", () => {
  const body = '{"role":"user","tenantId":"t1"}';
  const normalized = normalizeRequest(body);

  assert.equal(normalizedBody(normalized).role, "user");
  assert.throws(() => normalizedBody(body as never), /normalized request/i);
  assert.throws(() => evaluateWaf(body as never), /normalized request/i);
  assert.throws(() => routeRequest(body as never), /normalized request/i);
  assert.equal(Object.isFrozen(normalized), true);
  assert.equal(Object.isFrozen(normalized.document.value), true);
  assert.equal("rawBody" in normalized, false);

  const decision = admitNormalized(normalized);
  assert.equal(decision.allow, true);
  assert.strictEqual(decision.normalized, normalized);
});

test("a request gate shares options but keeps one normalized object per admission", () => {
  const gate = createRequestGate({
    duplicatePolicy: "last",
    signature: { required: false },
  });
  const body = '{"role":"user","tenantId":"t1","meta":{"x":1,"x":2}}';
  const decision = gate.admit(body);
  assert.equal(decision.allow, true);
  assert.equal(decision.consistent, true);
  assert.equal(gate.waf(decision.normalized!).allow, true);
  assert.equal(gate.route(decision.normalized!).allow, true);
});

test("policy options are accepted as the second argument", () => {
  const body = '{"trace":"one","trace":"two"}';
  const first = admitRequest(body, { duplicatePolicy: "first" });
  assert.equal(first.allow, true);
  assert.equal(first.normalized?.document.value.trace, "one");

  const gate = createRequestGate();
  const gateFirst = gate.admit(body, { duplicatePolicy: "first" });
  assert.equal(gateFirst.allow, true);
  assert.equal(gateFirst.normalized?.document.value.trace, "one");

  const normalized = normalizeRequest(body);
  assert.equal(admitRequest(normalized).allow, true);
});

test("duplicates hidden in a discarded duplicate value are still audited", () => {
  const body =
    '{"config":{"role":"user","role":"admin"},"config":{"ok":true}}';
  const decision = admitRequest(body);

  assert.equal(decision.allow, false);
  assert.equal(decision.consistent, true);
  assert.deepEqual(decision.normalized?.document.value.config, { ok: true });
  assert.ok(
    decision.normalized?.duplicates.some(
      (duplicate) =>
        duplicate.path === "$.config.role" && duplicate.protected,
    ),
  );
});

test("normalized values never trigger prototype pollution", () => {
  const normalized = normalizeRequest(
    '{"__proto__":{"polluted":true},"constructor":{"x":1}}',
  );
  const value = normalized.document.value as Record<string, unknown>;

  assert.equal(({} as { polluted?: boolean }).polluted, undefined);
  assert.equal(Object.getPrototypeOf(value), Object.prototype);
  assert.deepEqual(Object.keys(value), ["__proto__", "constructor"]);
  assert.equal(
    Object.getOwnPropertyDescriptor(value, "__proto__")?.value.polluted,
    true,
  );
});

test("Node-style middleware attaches only the normalized body", () => {
  const middleware = createAdmissionMiddleware();
  const request: Record<string, unknown> = {
    rawBody: '{"role":"user","tenantId":"t1"}',
    headers: {},
  };
  let nextCalls = 0;
  let responseBody = "";
  const response = {
    statusCode: 0,
    status(code: number) {
      this.statusCode = code;
      return this;
    },
    setHeader() {
      return undefined;
    },
    end(body?: string) {
      responseBody = body ?? "";
      return undefined;
    },
  };

  middleware(request, response, () => {
    nextCalls += 1;
  });
  assert.equal(nextCalls, 1);
  assert.equal(
    (request.body as Record<string, unknown>).role,
    "user",
  );
  assert.strictEqual(
    request.normalizedRequest,
    (request.admission as { normalized: unknown }).normalized,
  );

  const denied: Record<string, unknown> = {
    rawBody: '{"role":"user","role":"admin"}',
    headers: {},
    body: "untouched",
  };
  nextCalls = 0;
  middleware(denied, response, () => {
    nextCalls += 1;
  });
  assert.equal(nextCalls, 0);
  assert.equal(response.statusCode, 400);
  assert.equal(denied.body, "untouched");
  assert.match(responseBody, /PROTECTED_KEY_DUPLICATE/);

  const alreadyParsed: Record<string, unknown> = {
    body: { role: "user" },
    headers: {},
  };
  nextCalls = 0;
  middleware(alreadyParsed, response, () => {
    nextCalls += 1;
  });
  assert.equal(nextCalls, 0);
  assert.match(responseBody, /RAW_BODY_UNAVAILABLE/);
});
