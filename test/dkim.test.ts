import assert from "node:assert/strict";
import { test } from "node:test";

import {
  DkimHeaderCoverageError,
  REQUIRED_UNSUBSCRIBE_DKIM_HEADERS,
  assertDkimCoversUnsubscribeHeaders,
  buildOutboundEmail,
  isDkimCoverageValid,
} from "../index.ts";

const ONE_CLICK_HEADERS = {
  "List-Unsubscribe": "<https://example.com/unsubscribe/list?token=abc>",
  "List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
};

test("email construction fails when DKIM omits List-Unsubscribe", () => {
  const email = {
    from: "sender@example.com",
    to: "alice@example.com",
    subject: "Hi",
    headers: ONE_CLICK_HEADERS,
    dkim: { signedHeaders: ["From", "To", "Subject", "List-Unsubscribe-Post"] },
  };

  assert.throws(
    () => buildOutboundEmail(email),
    (error: unknown) =>
      error instanceof DkimHeaderCoverageError &&
      error.missingHeaders.includes("List-Unsubscribe"),
  );
});

test("email construction fails when DKIM omits List-Unsubscribe-Post", () => {
  const email = {
    headers: ONE_CLICK_HEADERS,
    dkim: { signedHeaders: ["From", "To", "Subject", "List-Unsubscribe"] },
  };

  assert.throws(
    () => buildOutboundEmail(email),
    (error: unknown) =>
      error instanceof DkimHeaderCoverageError &&
      error.missingHeaders.includes("List-Unsubscribe-Post"),
  );
});

test("DKIM header names are matched case-insensitively and h= style is accepted", () => {
  const email = {
    headers: ONE_CLICK_HEADERS,
    dkim: {
      h: "from:to:subject:list-unsubscribe:list-unsubscribe-post",
    },
  };

  const built = buildOutboundEmail(email);
  assert.deepEqual(built, email);
});

test("top-level signedHeaders and an explicit signed set are supported", () => {
  assert.equal(
    isDkimCoverageValid(
      ["From", "List-Unsubscribe", "List-Unsubscribe-Post"],
    ),
    true,
  );
  assert.equal(
    isDkimCoverageValid({ signedHeaders: ["From", "List-Unsubscribe"] }),
    false,
  );

  const email = {
    headers: ONE_CLICK_HEADERS,
    dkim: { signedHeaders: ["From"] },
  };
  assert.doesNotThrow(() =>
    assertDkimCoversUnsubscribeHeaders(email, [
      "List-Unsubscribe",
      "List-Unsubscribe-Post",
    ]),
  );
});

test("a raw DKIM-Signature h= tag is parsed", () => {
  const email = {
    headers: {
      ...ONE_CLICK_HEADERS,
      "DKIM-Signature":
        "v=1; a=rsa-sha256; d=example.com; s=mail; h=from:to:subject:list-unsubscribe:list-unsubscribe-post; bh=x; b=y",
    },
  };

  assert.doesNotThrow(() => buildOutboundEmail(email));
});

test("required DKIM header list is explicit", () => {
  assert.deepEqual(REQUIRED_UNSUBSCRIBE_DKIM_HEADERS, [
    "List-Unsubscribe",
    "List-Unsubscribe-Post",
  ]);
});
