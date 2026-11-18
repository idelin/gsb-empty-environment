import test from "node:test";
import assert from "node:assert/strict";
import { validateAccountsRequest } from "../index.ts";
import { expectError, expectOk } from "./helpers.ts";

const ACCOUNTS_URL = "https://idp.example/fedcm/accounts?client_id=rp-one";

test("accepts a browser-driven FedCM accounts request", () => {
  const validated = expectOk(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: { "Sec-Fetch-Dest": "webidentity" },
    }),
  );

  assert.equal(validated.clientId, "rp-one");
  assert.equal(validated.method, "GET");
});

test("accepts case-insensitive destination values and a Fetch Headers object", () => {
  const headers = new Headers();
  headers.set("Sec-Fetch-Dest", "WebIdentity");
  expectOk(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers,
    }),
  );
});

test("rejects a page fetch because Sec-Fetch-Dest is not webidentity", () => {
  const error = expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: {
        "sec-fetch-dest": "empty",
        origin: "https://attacker.example",
        referer: "https://attacker.example/attack.html",
      },
    }),
    "invalid_sec_fetch_dest",
    403,
  );

  assert.match(error.message, /webidentity/);
});

test("rejects requests with a missing Sec-Fetch-Dest", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: {},
    }),
    "missing_sec_fetch_dest",
    403,
  );
});

test("rejects every non-webidentity destination", () => {
  for (const destination of [
    "empty",
    "document",
    "iframe",
    "image",
    "script",
    "webidentity, webidentity",
  ]) {
    expectError(
      validateAccountsRequest({
        method: "GET",
        url: ACCOUNTS_URL,
        headers: { "sec-fetch-dest": destination },
      }),
      "invalid_sec_fetch_dest",
    );
  }
});

test("rejects an empty or duplicated Sec-Fetch-Dest", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: { "sec-fetch-dest": "" },
    }),
    "invalid_sec_fetch_dest",
  );

  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: {
        "sec-fetch-dest": ["webidentity", "webidentity"],
      },
    }),
    "invalid_sec_fetch_dest",
  );
});

test("rejects Origin even when the destination is correct", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: {
        "sec-fetch-dest": "webidentity",
        origin: "https://rp.example",
      },
    }),
    "unexpected_origin",
    403,
  );
});

test("rejects Referer even when the destination is correct", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: {
        "sec-fetch-dest": "webidentity",
        referer: "https://rp.example/callback",
      },
    }),
    "unexpected_referer",
    403,
  );
});

test("rejects even empty Origin and Referer headers", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: { "sec-fetch-dest": "webidentity", origin: "" },
    }),
    "unexpected_origin",
  );

  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: { "sec-fetch-dest": "webidentity", referer: "" },
    }),
    "unexpected_referer",
  );
});

test("rejects redirected requests reported by the adapter", () => {
  expectError(
    validateAccountsRequest({
      method: "GET",
      url: ACCOUNTS_URL,
      headers: { "sec-fetch-dest": "webidentity" },
      redirected: true,
    }),
    "redirect_not_allowed",
    403,
  );

  expectError(
    validateAccountsRequest(
      {
        method: "GET",
        url: ACCOUNTS_URL,
        headers: { "sec-fetch-dest": "webidentity" },
      },
      { redirectCount: 1 },
    ),
    "redirect_not_allowed",
  );
});

test("rejects non-GET accounts requests", () => {
  expectError(
    validateAccountsRequest({
      method: "POST",
      url: ACCOUNTS_URL,
      headers: { "sec-fetch-dest": "webidentity" },
    }),
    "method_not_allowed",
    405,
  );
});
