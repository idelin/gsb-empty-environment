import assert from "node:assert/strict";
import { test } from "node:test";

import { COEPPolicyError } from "./index.ts";
import { COEPResourceLoader } from "./resource-loader.ts";

test("resource loader strips identity under credentialless", async () => {
  let credentials: RequestCredentials | undefined;
  const foreign = new Response("remote", {
    headers: { "Set-Cookie": "session=foreign" },
  });

  const loader = new COEPResourceLoader({
    coep: "credentialless",
    origin: "https://app.example",
    fetch: async (_input, init) => {
      credentials = init?.credentials;
      return foreign;
    },
  });

  const response = await loader.load("https://cdn.example/data", {
    mode: "no-cors",
  });

  assert.equal(credentials, "omit");
  assert.equal(response.type, "opaque");
  assert.equal(response.body, null);
  assert.deepEqual([...response.headers], []);
});

test("resource loader rejects CORP-less responses under require-corp", async () => {
  const loader = new COEPResourceLoader({
    coep: "require-corp",
    origin: "https://app.example",
    fetch: async () => new Response("remote"),
  });

  await assert.rejects(
    loader.load("https://cdn.example/data", { mode: "no-cors" }),
    COEPPolicyError,
  );
});

test("resource loader preserves same-origin CORP responses", async () => {
  const local = new Response("local", {
    headers: { "Cross-Origin-Resource-Policy": "same-origin" },
  });
  const loader = new COEPResourceLoader({
    coep: "require-corp",
    origin: "https://app.example",
    fetch: async () => local,
  });

  const response = await loader.load("https://app.example/data", {
    mode: "no-cors",
  });

  assert.equal(response, local);
});
