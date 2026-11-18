import test from "node:test";
import assert from "node:assert/strict";
import { validateClientMetadataRequest } from "../index.ts";
import { expectError, expectOk } from "./helpers.ts";

test("accepts a credentialless client_metadata request", () => {
  const validated = expectOk(
    validateClientMetadataRequest({
      method: "GET",
      url: "https://idp.example/fedcm/client_metadata?client_id=rp-one",
      headers: { accept: "application/json" },
    }),
  );

  assert.equal(validated.endpoint, "client_metadata");
  assert.equal(validated.headers.has("cookie"), false);
});

test("rejects client_metadata requests carrying a Cookie", () => {
  const error = expectError(
    validateClientMetadataRequest({
      method: "GET",
      url: "https://idp.example/fedcm/client_metadata?client_id=rp-one",
      headers: { cookie: "session=alice" },
    }),
    "unexpected_cookie",
    400,
  );

  assert.match(error.message, /Cookie/);
});

test("rejects Cookie headers regardless of case or empty value", () => {
  expectError(
    validateClientMetadataRequest({
      method: "GET",
      headers: { COOKIE: "session=alice" },
    }),
    "unexpected_cookie",
  );

  expectError(
    validateClientMetadataRequest({
      method: "GET",
      headers: { cookie: "" },
    }),
    "unexpected_cookie",
  );
});

test("can explicitly require a GET client_metadata request", () => {
  expectError(
    validateClientMetadataRequest(
      { method: "POST", headers: {} },
      { requireGet: true },
    ),
    "method_not_allowed",
    405,
  );
});
