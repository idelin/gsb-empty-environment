import test from "node:test";
import assert from "node:assert/strict";
import {
  createIdAssertionClaimsFromRequest,
  validateIdAssertionRequest,
} from "../index.ts";
import { expectError, expectOk } from "./helpers.ts";

const metadata = [
  { client_id: "rp-one", origins: ["https://rp.example"] },
  { client_id: "rp-two", origins: ["https://other.example"] },
];

const alice = {
  id: "alice",
  email: "alice@example.com",
  email_verified: true,
  name: "Alice",
};

const bob = {
  id: "bob",
  email: "bob@example.com",
  name: "Bob",
};

const aliceSession = { accounts: [alice] };

function idRequest(overrides: Record<string, unknown> = {}) {
  return {
    method: "POST",
    url: "https://idp.example/fedcm/id_assertion",
    headers: {
      origin: "https://rp.example",
      cookie: "session=alice",
    },
    body: new URLSearchParams({
      client_id: "rp-one",
      account_id: "alice",
      disclosure_text_shown: "true",
    }),
    ...overrides,
  };
}

test("accepts a POST from a registered origin with an owned account", () => {
  const validated = expectOk(
    validateIdAssertionRequest(idRequest(), {
      clientMetadata: metadata,
      session: aliceSession,
    }),
  );

  assert.equal(validated.origin, "https://rp.example");
  assert.equal(validated.clientId, "rp-one");
  assert.equal(validated.account, alice);
  assert.equal(validated.accountId, "alice");
  assert.equal(validated.disclosureTextShown, true);
  assert.equal(validated.emailDisclosureAllowed, true);
});

test("accepts JSON form and object bodies", () => {
  expectOk(
    validateIdAssertionRequest(
      idRequest({
        body: JSON.stringify({
          client_id: "rp-one",
          account_id: "alice",
          disclosure_text_shown: true,
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
  );

  expectOk(
    validateIdAssertionRequest(
      idRequest({
        body: {
          client_id: "rp-one",
          account_id: "alice",
          disclosure_text_shown: "true",
        },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
  );
});

test("normalizes default ports when comparing Origin to metadata", () => {
  const validated = expectOk(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          origin: "https://rp.example:443",
          cookie: "session=alice",
        },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
  );

  assert.equal(validated.origin, "https://rp.example");
});

test("rejects an attacker page that uses a different Origin", () => {
  const error = expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          origin: "https://attacker.example",
          cookie: "session=alice",
        },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "origin_not_allowed",
    403,
  );

  assert.match(error.message, /not registered/);
});

test("rejects another RP's registered origin used for a different client_id", () => {
  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          origin: "https://other.example",
          cookie: "session=alice",
        },
        body: new URLSearchParams({
          client_id: "rp-one",
          account_id: "alice",
          disclosure_text_shown: "true",
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "origin_not_allowed",
    403,
  );
});

test("rejects an account_id that belongs to another session", () => {
  const error = expectError(
    validateIdAssertionRequest(
      idRequest({
        body: new URLSearchParams({
          client_id: "rp-one",
          account_id: "bob",
          disclosure_text_shown: "true",
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "account_not_found",
    403,
  );

  assert.match(error.message, /does not belong/);
});

test("rejects a missing Cookie and an unknown session", () => {
  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: { origin: "https://rp.example" },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "missing_cookie",
    401,
  );

  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          origin: "https://rp.example",
          cookie: "session=unknown",
        },
      }),
      {
        clientMetadata: metadata,
        session: (cookie) =>
          cookie === "session=alice" ? aliceSession : undefined,
      },
    ),
    "session_not_found",
    401,
  );
});

test("resolves the session from the Cookie header", () => {
  const validated = expectOk(
    validateIdAssertionRequest(idRequest(), {
      clientMetadata: metadata,
      session: (cookie) =>
        cookie === "session=alice" ? aliceSession : undefined,
    }),
  );

  assert.equal(validated.session, aliceSession);
});

test("rejects non-POST requests", () => {
  expectError(
    validateIdAssertionRequest(idRequest({ method: "GET" }), {
      clientMetadata: metadata,
      session: aliceSession,
    }),
    "method_not_allowed",
    405,
  );
});

test("rejects missing, malformed and duplicate Origins", () => {
  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: { cookie: "session=alice" },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "missing_origin",
    403,
  );

  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: { origin: "null", cookie: "session=alice" },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "invalid_origin",
  );

  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: { origin: "https://rp.example/path", cookie: "session=alice" },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "invalid_origin",
  );

  expectError(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          origin: ["https://rp.example", "https://attacker.example"],
          cookie: "session=alice",
        },
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "multiple_origins",
  );
});

test("rejects unknown clients and wildcard metadata", () => {
  expectError(
    validateIdAssertionRequest(
      idRequest({
        body: new URLSearchParams({
          client_id: "unknown",
          account_id: "alice",
          disclosure_text_shown: "true",
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
    "unknown_client",
  );

  expectError(
    validateIdAssertionRequest(idRequest(), {
      clientMetadata: [{ client_id: "rp-one", origins: ["*"] }],
      session: aliceSession,
    }),
    "origin_not_allowed",
  );
});

test("supports a metadata lookup function and the accounts convenience option", () => {
  expectOk(
    validateIdAssertionRequest(idRequest(), {
      clientMetadata: (clientId) =>
        metadata.find((entry) => entry.client_id === clientId),
      accounts: [alice],
    }),
  );
});

test("optionally requires Sec-Fetch-Dest: webidentity", () => {
  expectError(
    validateIdAssertionRequest(idRequest(), {
      clientMetadata: metadata,
      session: aliceSession,
      requireWebIdentity: true,
    }),
    "missing_sec_fetch_dest",
  );

  expectOk(
    validateIdAssertionRequest(
      idRequest({
        headers: {
          "sec-fetch-dest": "webidentity",
          origin: "https://rp.example",
          cookie: "session=alice",
        },
      }),
      {
        clientMetadata: metadata,
        session: aliceSession,
        requireWebIdentity: true,
      },
    ),
  );
});

test("builds assertion claims without email when disclosure is missing", () => {
  const validated = expectOk(
    validateIdAssertionRequest(
      idRequest({
        body: new URLSearchParams({
          client_id: "rp-one",
          account_id: "alice",
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
  );

  assert.equal(validated.disclosureTextShown, false);
  const claims = createIdAssertionClaimsFromRequest(validated);
  assert.equal(claims.sub, "alice");
  assert.equal(claims.name, "Alice");
  assert.equal("email" in claims, false);
  assert.equal("email_verified" in claims, false);
});

test("does not treat disclosure_shown as an alias for disclosure_text_shown", () => {
  const validated = expectOk(
    validateIdAssertionRequest(
      idRequest({
        body: new URLSearchParams({
          client_id: "rp-one",
          account_id: "alice",
          disclosure_shown: "true",
        }),
      }),
      { clientMetadata: metadata, session: aliceSession },
    ),
  );

  assert.equal(validated.disclosureTextShown, false);
  const claims = createIdAssertionClaimsFromRequest(validated);
  assert.equal("email" in claims, false);
});

test("rejects malformed id_assertion bodies", () => {
  expectError(
    validateIdAssertionRequest(idRequest({ body: undefined }), {
      clientMetadata: metadata,
      session: aliceSession,
    }),
    "invalid_body",
    400,
  );

  expectError(
    validateIdAssertionRequest(idRequest({ body: "{not json" }), {
      clientMetadata: metadata,
      session: aliceSession,
    }),
    "invalid_body",
  );
});
