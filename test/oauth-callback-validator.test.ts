import assert from "node:assert/strict";
import test from "node:test";
import {
  createOAuthCallbackValidator,
  createPkceChallenge,
  OAuthCallbackError,
} from "../index.ts";

const REGISTERED = "https://app.example.com/oauth/callback";
const VERIFIER = "a".repeat(64);

function setup() {
  const validator = createOAuthCallbackValidator();
  validator.registerClient({
    clientId: "web-client",
    redirectUris: [REGISTERED],
  });
  return validator;
}

test("authorization codes are bound to client, redirect URI, PKCE, and nonce", () => {
  const validator = setup();
  const issued = validator.authorize({
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
    state: "state-456",
  });

  assert.equal(issued.redirectUri, REGISTERED);
  assert.equal(issued.state, "state-456");

  const grant = validator.exchange({
    code: issued.code,
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeVerifier: VERIFIER,
    nonce: "nonce-123",
  });
  assert.equal(grant.redirectUri, REGISTERED);
  assert.equal(grant.clientId, "web-client");
  assert.equal(grant.nonce, "nonce-123");
});

test("authorization codes are single-use", () => {
  const validator = setup();
  const issued = validator.authorize({
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
  });
  const input = {
    code: issued.code,
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeVerifier: VERIFIER,
    nonce: "nonce-123",
  };
  validator.exchange(input);
  assert.throws(
    () => validator.exchange(input),
    (error: unknown) =>
      error instanceof OAuthCallbackError && error.code === "invalid_grant",
  );
});

test("wrong client, verifier, nonce, or redirect URI cannot redeem the code", () => {
  const validator = setup();
  validator.registerClient({
    clientId: "other-client",
    redirectUris: [REGISTERED],
  });

  const base = {
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
  };

  const cases = [
    { clientId: "other-client", codeVerifier: VERIFIER, nonce: "nonce-123", redirectUri: REGISTERED },
    { clientId: "web-client", codeVerifier: "b".repeat(64), nonce: "nonce-123", redirectUri: REGISTERED },
    { clientId: "web-client", codeVerifier: VERIFIER, nonce: "wrong-nonce", redirectUri: REGISTERED },
    {
      clientId: "web-client",
      codeVerifier: VERIFIER,
      nonce: "nonce-123",
      redirectUri: "https://app.example.com.attacker.io/oauth/callback",
    },
    {
      clientId: "web-client",
      codeVerifier: VERIFIER,
      nonce: "nonce-123",
      redirectUri: "https://app.example.com/oauth/callback/../callback",
    },
  ];

  for (const failing of cases) {
    const issued = validator.authorize(base);
    assert.throws(
      () => validator.exchange({ code: issued.code, ...failing }),
      (error: unknown) =>
        error instanceof OAuthCallbackError && error.code === "invalid_grant",
      JSON.stringify(failing),
    );
  }
});

test("an attacker redirect URI never receives an authorization code", () => {
  const validator = setup();
  for (const redirectUri of [
    "https://app.example.com.attacker.io/oauth/callback",
    "https://evil-app.example.com/oauth/callback",
    "https://app.example.com/oauth/../oauth/callback",
    "https://app.example.com:8443/oauth/callback",
    "https://\u0430pp.example.com/oauth/callback",
  ]) {
    assert.throws(
      () =>
        validator.authorize({
          clientId: "web-client",
          redirectUri,
          codeChallenge: createPkceChallenge(VERIFIER),
          nonce: "nonce-123",
        }),
      (error: unknown) =>
        error instanceof OAuthCallbackError && error.code === "invalid_redirect_uri",
      redirectUri,
    );
  }
});

test("a wildcard template code is bound to the exact expanded URI", () => {
  const validator = createOAuthCallbackValidator();
  validator.registerClient({
    clientId: "wildcard-client",
    redirectUris: ["https://app.example.com/oauth/*/callback"],
  });

  const issued = validator.authorize({
    clientId: "wildcard-client",
    redirectUri: "https://app.example.com/oauth/tenant-one/callback",
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
  });
  assert.equal(
    issued.redirectUri,
    "https://app.example.com/oauth/tenant-one/callback",
  );

  assert.throws(
    () =>
      validator.exchange({
        code: issued.code,
        clientId: "wildcard-client",
        redirectUri: "https://app.example.com/oauth/tenant-two/callback",
        codeVerifier: VERIFIER,
        nonce: "nonce-123",
      }),
    (error: unknown) =>
      error instanceof OAuthCallbackError && error.code === "invalid_grant",
  );

  const grant = validator.exchange({
    code: issued.code,
    clientId: "wildcard-client",
    redirectUri: "https://app.example.com/oauth/tenant-one/callback",
    codeVerifier: VERIFIER,
    nonce: "nonce-123",
  });
  assert.equal(
    grant.redirectUri,
    "https://app.example.com/oauth/tenant-one/callback",
  );
});

test("registration policy gates loopback and custom schemes", () => {
  const validator = createOAuthCallbackValidator();
  assert.throws(
    () =>
      validator.registerClient({
        clientId: "native",
        redirectUris: ["http://127.0.0.1:3000/callback"],
      }),
    (error: unknown) =>
      error instanceof OAuthCallbackError && error.code === "invalid_redirect_uri",
  );

  validator.registerClient({
    clientId: "native",
    redirectUris: [
      "http://127.0.0.1:3000/callback",
      "com.example.app://oauth/callback",
    ],
    allowLoopback: true,
    allowCustomSchemes: ["com.example.app"],
  });

  assert.equal(
    validator.isRedirectUriAllowed("native", "http://127.0.0.1:3000/callback"),
    true,
  );
  assert.equal(
    validator.isRedirectUriAllowed("native", "http://127.0.0.1:3001/callback"),
    false,
  );
  assert.equal(
    validator.isRedirectUriAllowed("native", "com.example.app://oauth/callback"),
    true,
  );
});

test("accepts default policy and clients in the constructor", () => {
  const validator = createOAuthCallbackValidator({
    allowLoopback: true,
    allowCustomSchemes: ["com.example.app"],
    clients: [
      {
        clientId: "native",
        redirectUris: [
          "http://127.0.0.1:3000/callback",
          "com.example.app:/oauth2redirect",
        ],
      },
    ],
  });

  assert.equal(
    validator.isRedirectUriAllowed("native", "http://127.0.0.1:3000/callback"),
    true,
  );
  assert.equal(
    validator.isRedirectUriAllowed("native", "com.example.app:/oauth2redirect"),
    true,
  );
});

test("codes expire and changing registration invalidates outstanding codes", () => {
  let now = 1_000;
  const validator = createOAuthCallbackValidator({
    now: () => now,
    defaultCodeTtlMs: 100,
  });
  validator.registerClient({
    clientId: "web-client",
    redirectUris: [REGISTERED],
  });

  const expiring = validator.authorize({
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
  });
  now += 101;
  assert.throws(
    () =>
      validator.exchange({
        code: expiring.code,
        clientId: "web-client",
        redirectUri: REGISTERED,
        codeVerifier: VERIFIER,
        nonce: "nonce-123",
      }),
    OAuthCallbackError,
  );

  now = 2_000;
  const outstanding = validator.authorize({
    clientId: "web-client",
    redirectUri: REGISTERED,
    codeChallenge: createPkceChallenge(VERIFIER),
    nonce: "nonce-123",
  });
  validator.registerClient({
    clientId: "web-client",
    redirectUris: ["https://app.example.com/oauth/new-callback"],
  });
  assert.throws(
    () =>
      validator.exchange({
        code: outstanding.code,
        clientId: "web-client",
        redirectUri: REGISTERED,
        codeVerifier: VERIFIER,
        nonce: "nonce-123",
      }),
    (error: unknown) =>
      error instanceof OAuthCallbackError && error.code === "invalid_grant",
  );
});
