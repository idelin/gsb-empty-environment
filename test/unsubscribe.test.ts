import assert from "node:assert/strict";
import { createHmac } from "node:crypto";
import { test } from "node:test";

import {
  ONE_CLICK_BODY,
  buildUnsubscribeTokenMessage,
  createOneClickUnsubscribeHandler,
  createOneClickUnsubscribeUrl,
  createUnsubscribeToken,
  inspectUnsubscribeToken,
  verifyUnsubscribeToken,
} from "../index.ts";

const SECRET = "unsubscribe-test-secret";
const NOW = 1_700_000_000;
const RECIPIENT = "alice+tag@example.com";
const LIST_ID = "product-updates/2026";

function makeHandler(
  calls: Array<{ listId: string; recipient: string }>,
) {
  return createOneClickUnsubscribeHandler({
    secret: SECRET,
    now: () => NOW,
    onUnsubscribe: (event) => {
      calls.push({ listId: event.listId, recipient: event.recipient });
    },
  });
}

function makeUrl(
  listId = LIST_ID,
  recipient = RECIPIENT,
  expiresAt = NOW + 3600,
) {
  return createOneClickUnsubscribeUrl({
    baseUrl: "https://example.com",
    secret: SECRET,
    listId,
    recipient,
    expiresAt,
  });
}

function post(url: string, body = ONE_CLICK_BODY) {
  return new Request(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    },
    body,
  });
}

test("token exactly signs recipient, NUL, list ID, NUL, decimal expiry", () => {
  const expiresAt = NOW + 3600;
  const token = createUnsubscribeToken({
    secret: SECRET,
    recipient: RECIPIENT,
    listId: LIST_ID,
    expiresAt,
  });
  const message = buildUnsubscribeTokenMessage(
    RECIPIENT,
    LIST_ID,
    String(expiresAt),
  );
  const expected = createHmac("sha256", SECRET)
    .update(message, "utf8")
    .digest("hex");

  assert.equal(token, expected);
  assert.equal(token.length, 64);
});

test("GET only renders a confirmation form and never changes state", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = makeUrl();
  const response = await handler(new Request(url, { method: "GET" }));
  const html = await response.text();

  assert.equal(response.status, 200);
  assert.match(response.headers.get("content-type") ?? "", /text\/html/);
  assert.match(html, /<form method="POST">/);
  assert.match(html, /name="List-Unsubscribe" value="One-Click"/);
  assert.equal(calls.length, 0);
});

test("exact RFC 8058 POST unsubscribes only once", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = makeUrl();

  const response = await handler(post(url));
  assert.equal(response.status, 200);
  assert.deepEqual(calls, [{ listId: LIST_ID, recipient: RECIPIENT }]);

  assert.equal(calls.length, 1);
});

test("POST rejects wrong body, extra fields, and wrong content type", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = makeUrl();

  const extra = await handler(
    post(url, `${ONE_CLICK_BODY}&extra=1`),
  );
  assert.equal(extra.status, 400);

  const newline = await handler(post(url, `${ONE_CLICK_BODY}\n`));
  assert.equal(newline.status, 400);

  const wrongType = await handler(
    new Request(url, {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: ONE_CLICK_BODY,
    }),
  );
  assert.equal(wrongType.status, 415);
  assert.equal(calls.length, 0);
});

test("GET and non-POST methods cannot unsubscribe", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = makeUrl();

  const get = await handler(new Request(url));
  assert.equal(get.status, 200);

  const put = await handler(new Request(url, { method: "PUT" }));
  assert.equal(put.status, 405);
  assert.equal(calls.length, 0);
});

test("a token cannot be moved to a different list ID in the path", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const original = new URL(makeUrl("private-list"));
  const changed = new URL(original);
  changed.pathname = "/unsubscribe/other-list";

  const response = await handler(post(changed.toString()));
  assert.equal(response.status, 400);
  assert.equal(calls.length, 0);
});

test("expired tokens are rejected", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = makeUrl(LIST_ID, RECIPIENT, NOW - 1);

  const response = await handler(post(url));
  assert.equal(response.status, 400);
  assert.equal(calls.length, 0);

  const validation = inspectUnsubscribeToken({
    secret: SECRET,
    recipient: RECIPIENT,
    listId: LIST_ID,
    expiresAt: NOW - 1,
    token: createUnsubscribeToken({
      secret: SECRET,
      recipient: RECIPIENT,
      listId: LIST_ID,
      expiresAt: NOW - 1,
    }),
    now: NOW,
  });
  assert.equal(validation.ok, false);
  assert.equal(validation.code, "expired");
});

test("changing the expiry text invalidates the token", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = new URL(makeUrl());
  url.searchParams.set("expires", String(NOW + 7200));

  const response = await handler(post(url.toString()));
  assert.equal(response.status, 400);
  assert.equal(calls.length, 0);
});

test("changing the recipient invalidates the token", async () => {
  const calls: Array<{ listId: string; recipient: string }> = [];
  const handler = makeHandler(calls);
  const url = new URL(makeUrl());
  url.searchParams.set("recipient", "mallory@example.com");

  const response = await handler(post(url.toString()));
  assert.equal(response.status, 400);
  assert.equal(calls.length, 0);
});

test("verify helper accepts and rejects the signed parameters", () => {
  const expiresAt = NOW + 30;
  const token = createUnsubscribeToken({
    secret: SECRET,
    recipient: RECIPIENT,
    listId: LIST_ID,
    expiresAt,
  });

  assert.equal(
    verifyUnsubscribeToken({
      secret: SECRET,
      recipient: RECIPIENT,
      listId: LIST_ID,
      expiresAt,
      token,
      now: NOW,
    }),
    true,
  );
  assert.equal(
    verifyUnsubscribeToken({
      secret: SECRET,
      recipient: "other@example.com",
      listId: LIST_ID,
      expiresAt,
      token,
      now: NOW,
    }),
    false,
  );
});

test("URL construction preserves plus-addressing and an encoded list ID", () => {
  const url = new URL(makeUrl());
  assert.equal(url.pathname, "/unsubscribe/product-updates%2F2026");
  assert.equal(url.searchParams.get("recipient"), RECIPIENT);
  assert.equal(url.searchParams.get("expires"), String(NOW + 3600));
  assert.match(url.searchParams.get("token") ?? "", /^[0-9a-f]{64}$/);
});
