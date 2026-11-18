import test from "node:test";
import assert from "node:assert/strict";
import {
  assertResponseNotRedirect,
  createIdAssertionClaimsFromRequest,
  validateAccountsRequest,
  validateClientMetadataRequest,
  validateIdAssertionRequest,
} from "../index.ts";

const accounts = [
  {
    id: "alice",
    email: "alice@example.com",
    email_verified: true,
    name: "Alice",
  },
  {
    id: "bob",
    email: "bob@example.com",
    name: "Bob",
  },
];

const metadata = [
  { client_id: "rp-one", origins: ["https://rp.example"] },
];

function accountsHandler(request) {
  const validation = validateAccountsRequest(request);
  if (!validation.ok) {
    return {
      status: validation.error.status,
      body: { error: validation.error.code },
    };
  }
  return { status: 200, body: { accounts } };
}

function idAssertionHandler(request, session) {
  const validation = validateIdAssertionRequest(request, {
    clientMetadata: metadata,
    session,
  });
  if (!validation.ok) {
    return {
      status: validation.error.status,
      body: { error: validation.error.code },
    };
  }

  return {
    status: 200,
    body: {
      token: "signed.jwt.value",
      claims: createIdAssertionClaimsFromRequest(validation.value),
    },
  };
}

function idRequest(overrides = {}) {
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

test("a page fetch cannot read the accounts endpoint", () => {
  const response = accountsHandler({
    method: "GET",
    url: "https://idp.example/fedcm/accounts?client_id=rp-one",
    headers: {
      "sec-fetch-dest": "empty",
      origin: "https://attacker.example",
      referer: "https://attacker.example/",
    },
  });

  assert.equal(response.status, 403);
  assert.deepEqual(response.body, { error: "invalid_sec_fetch_dest" });
});

test("the browser FedCM accounts request is served", () => {
  const response = accountsHandler({
    method: "GET",
    url: "https://idp.example/fedcm/accounts?client_id=rp-one",
    headers: { "sec-fetch-dest": "webidentity" },
  });

  assert.equal(response.status, 200);
  assert.deepEqual(response.body, { accounts });
});

test("id_assertion rejects a different Origin", () => {
  const response = idAssertionHandler(
    idRequest({
      headers: {
        origin: "https://attacker.example",
        cookie: "session=alice",
      },
    }),
    { accounts: [accounts[0]] },
  );

  assert.equal(response.status, 403);
  assert.deepEqual(response.body, { error: "origin_not_allowed" });
});

test("id_assertion rejects another user's account_id", () => {
  const response = idAssertionHandler(
    idRequest({
      body: new URLSearchParams({
        client_id: "rp-one",
        account_id: "bob",
        disclosure_text_shown: "true",
      }),
    }),
    { accounts: [accounts[0]] },
  );

  assert.equal(response.status, 403);
  assert.deepEqual(response.body, { error: "account_not_found" });
});

test("id_assertion omits email when disclosure_text_shown is missing", () => {
  const response = idAssertionHandler(
    idRequest({
      body: new URLSearchParams({
        client_id: "rp-one",
        account_id: "alice",
      }),
    }),
    { accounts: [accounts[0]] },
  );

  assert.equal(response.status, 200);
  assert.equal(response.body.claims.sub, "alice");
  assert.equal("email" in response.body.claims, false);
  assert.equal("email_verified" in response.body.claims, false);
});

test("id_assertion includes email only when disclosure is affirmative", () => {
  const response = idAssertionHandler(idRequest(), {
    accounts: [accounts[0]],
  });

  assert.equal(response.status, 200);
  assert.equal(response.body.claims.email, "alice@example.com");
});

test("client_metadata rejects a request carrying a Cookie", () => {
  const validation = validateClientMetadataRequest({
    method: "GET",
    url: "https://idp.example/fedcm/client_metadata?client_id=rp-one",
    headers: { cookie: "session=alice" },
  });

  assert.equal(validation.ok, false);
  if (validation.ok) throw new Error("expected rejection");
  assert.equal(validation.error.status, 400);
  assert.equal(validation.error.code, "unexpected_cookie");
});

test("handlers can reject redirect responses", () => {
  assert.throws(
    () => assertResponseNotRedirect(302),
    (error) =>
      error.code === "redirect_not_allowed" && error.status === 403,
  );
  assert.doesNotThrow(() => assertResponseNotRedirect(200));
});
