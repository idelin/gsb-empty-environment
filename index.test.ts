import assert from "node:assert/strict";
import { test } from "node:test";

import {
  COEPPolicyError,
  adjudicateResponse,
  enforceResponsePolicy,
} from "./index.ts";

test("credentialless makes cross-origin no-cors responses opaque", () => {
  const original = new Response("secret", {
    headers: {
      "Set-Cookie": "session=foreign",
      "X-Identity": "remote",
    },
  });

  const result = adjudicateResponse({
    coep: "credentialless",
    corp: null,
    crossOrigin: true,
    mode: "no-cors",
    response: original,
  });

  assert.equal(result.action, "opaque");
  assert.equal(result.opaque, true);
  assert.equal(result.credentialsStripped, true);
  assert.equal(result.response?.type, "opaque");
  assert.equal(result.response?.status, 0);
  assert.equal(result.response?.body, null);
  assert.deepEqual([...result.response!.headers], []);
  assert.notEqual(result.response, original);
});

test("require-corp blocks cross-origin no-cors responses without CORP", () => {
  const result = adjudicateResponse({
    coep: "require-corp",
    corp: null,
    crossOrigin: true,
    mode: "no-cors",
    response: new Response("remote"),
  });

  assert.equal(result.action, "block");
  assert.equal(result.blocked, true);
  assert.equal(result.response, null);

  assert.throws(
    () =>
      enforceResponsePolicy({
        coep: "require-corp",
        corp: null,
        crossOrigin: true,
        mode: "no-cors",
      }),
    COEPPolicyError,
  );
});

test("same-origin CORP responses are returned unchanged", () => {
  const original = new Response("local", {
    status: 201,
    headers: { "Cross-Origin-Resource-Policy": "same-origin" },
  });

  const result = adjudicateResponse({
    coep: "require-corp",
    corp: "same-origin",
    crossOrigin: false,
    mode: "no-cors",
    response: original,
  });

  assert.equal(result.action, "allow");
  assert.equal(result.response, original);
  assert.equal(result.response?.status, 201);
  assert.equal(
    result.response?.headers.get("Cross-Origin-Resource-Policy"),
    "same-origin",
  );
});

test("CORP cross-origin permits a cross-origin response", () => {
  const original = new Response("remote");
  const result = adjudicateResponse(
    "require-corp",
    "cross-origin",
    true,
    "no-cors",
  );

  assert.equal(result.action, "allow");

  const withResponse = adjudicateResponse(original, {
    coep: "credentialless",
    corp: "cross-origin",
    crossOrigin: true,
    mode: "no-cors",
  });

  assert.equal(withResponse.action, "allow");
  assert.equal(withResponse.response, original);
});

test("switching COEP values changes a CORP-less response disposition", () => {
  const context = {
    corp: null,
    crossOrigin: true,
    mode: "no-cors" as const,
  };

  const credentialless = adjudicateResponse({
    ...context,
    coep: "credentialless",
  });
  const requireCorp = adjudicateResponse({
    ...context,
    coep: "require-corp",
  });

  assert.equal(credentialless.action, "opaque");
  assert.equal(requireCorp.action, "block");
});

test("the library reads CORP from the supplied response", () => {
  const original = new Response("remote", {
    headers: { "Cross-Origin-Resource-Policy": "cross-origin" },
  });

  const result = adjudicateResponse(original, {
    coep: "require-corp",
    crossOrigin: true,
    mode: "no-cors",
  });

  assert.equal(result.action, "allow");
  assert.equal(result.response, original);
});
