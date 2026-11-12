import assert from "node:assert/strict";
import test from "node:test";
import {
  DEFAULT_RETENTION_WINDOW,
  InvalidLastEventIdError,
  MAX_LAST_EVENT_ID_BYTES,
  SseResume,
  extractLastEventIdRequest,
  formatResetEvent,
  formatSseEvent,
  issueEventId,
  parseEventId,
  resolveResume,
  verifyEventId,
  type ResumeDecision,
  type StreamIdentity,
} from "../index.ts";

const SECRET = "test-secret-that-is-not-production";
const TENANT_A = "tenant-a";
const TENANT_B = "tenant-b";
const STREAM = "orders";

const subscription: StreamIdentity = { tenant: TENANT_A, stream: STREAM };

function issue(
  sequence: number,
  identity: StreamIdentity = subscription,
): string {
  return issueEventId({ ...identity, sequence, secret: SECRET });
}

function decision(
  id: string | undefined,
  latestSequence: number,
  identity: StreamIdentity = subscription,
  retentionWindow?: number,
): ResumeDecision {
  return resolveResume({
    subscription: identity,
    secret: SECRET,
    latestSequence,
    headerLastEventId: id,
    retentionWindow,
  });
}

test("ID format is stream:sequence:32-hex and binds tenant/stream/sequence", () => {
  const id = issue(7);
  assert.match(id, /^orders:7:[0-9a-f]{32}$/);

  const parsed = parseEventId(id);
  assert.deepEqual(parsed, {
    stream: STREAM,
    sequence: 7,
    signature: id.split(":").at(-1),
  });

  assert.equal(verifyEventId(id, subscription, SECRET), true);
  assert.equal(
    verifyEventId(id, { tenant: TENANT_B, stream: STREAM }, SECRET),
    false,
  );
  assert.equal(
    verifyEventId(id, { tenant: TENANT_A, stream: "other" }, SECRET),
    false,
  );
  assert.equal(verifyEventId(id.replace(/:7:/, ":8:"), subscription, SECRET), false);
});

test("valid cursor resumes after the delivered event", () => {
  const id = issue(4);
  const result = decision(id, 10);
  assert.deepEqual(result, {
    action: "resume",
    nextSequence: 5,
    resetEvent: null,
  });
});

test("missing cursor starts at the tail without a reset", () => {
  assert.deepEqual(decision(undefined, 10), {
    action: "start",
    nextSequence: 11,
    resetEvent: null,
  });
  assert.deepEqual(decision(undefined, -1), {
    action: "start",
    nextSequence: 0,
    resetEvent: null,
  });
});

test("a cursor signed for another tenant resets instead of crossing tenants", () => {
  const otherTenantId = issue(4, { tenant: TENANT_B, stream: STREAM });
  const result = decision(otherTenantId, 10);

  assert.equal(result.action, "reset");
  assert.equal(result.nextSequence, 11);
  assert.equal(result.resetEvent?.event, "reset");
  assert.equal(result.resetEvent?.data, "");
  assert.equal("reason" in (result.resetEvent ?? {}), false);
});

test("a cursor for a different stream resets", () => {
  const otherStreamId = issue(4, { tenant: TENANT_A, stream: "invoices" });
  assert.equal(decision(otherStreamId, 10).action, "reset");
});

test("forged signatures reset without disclosing a reason", () => {
  const id = issue(4);
  const prefix = id.slice(0, id.lastIndexOf(":") + 1);

  for (const forged of [
    `${prefix}${"0".repeat(32)}`,
    `${prefix}${"z".repeat(32)}`,
    `${prefix}${"a".repeat(32)}`,
    `${prefix}short`,
  ]) {
    const result = decision(forged, 10);
    assert.equal(result.action, "reset");
    assert.equal(result.nextSequence, 11);
    assert.equal(result.resetEvent?.event, "reset");
    assert.equal(result.resetEvent?.data, "");
    assert.equal(JSON.stringify(result.resetEvent).includes("signature"), false);
  }
});

test("header and query cursors must be identical when both are present", () => {
  const first = issue(4);
  const second = issue(5);

  const rejected = resolveResume({
    subscription,
    secret: SECRET,
    latestSequence: 10,
    headerLastEventId: first,
    queryLastEventId: second,
  });
  assert.deepEqual(rejected, {
    action: "reject",
    statusCode: 400,
    nextSequence: null,
  });

  assert.equal(
    resolveResume({
      subscription,
      secret: SECRET,
      latestSequence: 10,
      headerLastEventId: first,
      queryLastEventId: first,
    }).action,
    "resume",
  );
});

test("repeated cursor values are rejected rather than selected", () => {
  const id = issue(4);
  const result = resolveResume({
    subscription,
    secret: SECRET,
    latestSequence: 10,
    headerLastEventId: [id, id],
  });
  assert.equal(result.action, "reject");
});

test("CR, LF, NUL, and over-200-byte IDs are rejected", () => {
  const id = issue(4);

  for (const raw of [
    `${id}\n`,
    `${id}\r`,
    `${id}\0`,
    `bad\r\ncursor:${"x".repeat(80)}`,
    "x".repeat(MAX_LAST_EVENT_ID_BYTES + 1),
    "é".repeat(101),
  ]) {
    assert.throws(() => parseEventId(raw), InvalidLastEventIdError);
    assert.equal(decision(raw, 10).action, "reject");
  }
});

test("a signed cursor of exactly 200 UTF-8 bytes is accepted", () => {
  // stream + ":" + "0" + ":" + 32-character hex signature = 165 + 3 + 32.
  const stream = "s".repeat(165);
  const identity = { tenant: TENANT_A, stream };
  const id = issue(0, identity);

  assert.equal(Buffer.byteLength(id, "utf8"), MAX_LAST_EVENT_ID_BYTES);
  assert.deepEqual(parseEventId(id), {
    stream,
    sequence: 0,
    signature: id.slice(-32),
  });
  assert.equal(decision(id, 0, identity).action, "resume");
});

test("retention window keeps exactly the newest 1000 sequences", () => {
  assert.equal(DEFAULT_RETENTION_WINDOW, 1000);
  const latest = 2500;
  const oldest = latest - DEFAULT_RETENTION_WINDOW + 1;

  assert.deepEqual(decision(issue(oldest), latest), {
    action: "resume",
    nextSequence: oldest + 1,
    resetEvent: null,
  });

  const evicted = decision(issue(oldest - 1), latest);
  assert.equal(evicted.action, "reset");
  assert.equal(evicted.nextSequence, latest + 1);

  assert.equal(decision(issue(latest - 1), latest).action, "resume");
  assert.equal(decision(issue(latest), latest).action, "resume");
  assert.equal(decision(issue(latest + 1), latest).action, "reset");
});

test("a valid signed cursor resets when the stream has no retained events", () => {
  const result = decision(issue(0), -1);
  assert.equal(result.action, "reset");
  assert.equal(result.nextSequence, 0);
});

test("reset event contains only the private reset marker", () => {
  assert.equal(formatResetEvent(), "event: reset\ndata: \n\n");
  assert.doesNotMatch(formatResetEvent(), /reason|signature|sequence/i);
});

test("data and id newlines are emitted as same-field continuation lines", () => {
  const event = {
    event: "message",
    id: "safe\nretry: 1\nevent: forged",
    data: "hello\r\nworld\rline\n\ndata: forged\nevent: forged\nretry: 0",
  };
  const output = formatSseEvent(event);
  const lines = output.slice(0, -2).split("\n");

  const fieldNames = lines.map((line) => line.slice(0, line.indexOf(": ")));
  assert.deepEqual(
    fieldNames,
    [
      "event",
      "id",
      "id",
      "id",
      "data",
      "data",
      "data",
      "data",
      "data",
      "data",
      "data",
    ],
  );
  assert.equal(output.includes("\nretry: 1"), false);
  assert.equal(output.includes("\nevent: forged\n"), false);
  assert.match(output, /^event: message\n/);
  assert.match(output, /\ndata: hello\n/);
  assert.match(output, /\ndata: data: forged\n/);
  assert.match(output, /\ndata: event: forged\n/);
  assert.match(output, /\ndata: retry: 0\n/);
});

test("serializer ignores unknown object fields and JSON-escapes payload newlines", () => {
  const output = formatSseEvent({
    id: 1,
    data: { text: "a\nb", extra: "event: forged" },
    // @ts-expect-error Unknown fields must never become SSE fields.
    reason: "sensitive",
  });

  assert.equal(output, 'id: 1\ndata: {"text":"a\\nb","extra":"event: forged"}\n\n');
  assert.equal(output.includes("reason"), false);
});

test("HTTP adapter reads both sources and preserves ambiguity", () => {
  const id = issue(4);
  assert.deepEqual(
    extractLastEventIdRequest({
      headers: new Headers({ "Last-Event-ID": id }),
      url: `https://example.test/events?lastEventId=${encodeURIComponent(id)}`,
    }),
    { headerLastEventId: id, queryLastEventId: id },
  );

  const differing = extractLastEventIdRequest({
    headers: { "last-event-id": id },
    url: `https://example.test/events?lastEventId=${encodeURIComponent(issue(5))}`,
  });
  assert.equal(resolveResume({ ...differing, subscription, secret: SECRET, latestSequence: 10 }).action, "reject");
});

test("stateful wrapper signs and resolves with its configured policy", () => {
  const helper = new SseResume({ secret: SECRET, retentionWindow: 3 });
  const id = helper.issueEventId(subscription, 7);
  assert.equal(helper.resolve(subscription, { headerLastEventId: id }, 9).action, "resume");
  assert.equal(helper.resolve(subscription, { headerLastEventId: id }, 10).action, "reset");
});
