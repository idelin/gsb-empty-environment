import { test } from "node:test";
import assert from "node:assert/strict";

import {
  contentIdOf,
  expandSmallGraph,
  resolveContext,
} from "../src/index.ts";
import { buildHarness, expectReject } from "./helpers.ts";

test("small-graph reference expands vocab, base, @id and @reverse without cache", async () => {
  const root = {
    "@vocab": "https://cred.test/#",
    "@base": "https://cred.test/base/",
    type: { "@id": "https://cred.test/#type", "@type": "@vocab", "@protected": true },
    issuer: { "@id": "https://cred.test/#issuer", "@type": "@id" },
    members: { "@reverse": "https://cred.test/#memberOf" },
  };
  const harness = await buildHarness([root]);
  const resolution = resolveContext({
    rootCid: contentIdOf(root),
    store: harness.store,
    allowed: harness.allowed,
  });

  const expanded = expandSmallGraph(
    {
      type: "VerifiableCredential",
      issuer: "did:web:issuer.example",
      members: "org-1",
    },
    resolution,
  );

  assert.equal(
    expanded["https://cred.test/#type"],
    "https://cred.test/#VerifiableCredential",
  );
  assert.equal(expanded["https://cred.test/#issuer"], "did:web:issuer.example");
  const reverse = expanded["@reverse:https://cred.test/#memberOf"];
  assert.equal(reverse, "org-1");
});

test("small-graph reference rejects undeclared terms", async () => {
  const root = { "@vocab": "https://cred.test/#", declared: "https://cred.test/#declared" };
  const harness = await buildHarness([root]);
  const resolution = resolveContext({
    rootCid: contentIdOf(root),
    store: harness.store,
    allowed: harness.allowed,
  });
  expectReject(
    () => expandSmallGraph({ undeclared: "x" }, resolution),
    "UNKNOWN_KEYWORD_REWRITE",
  );
});

test("nested @nest alias conflict surfaces through the no-cache reference", async () => {
  const shared = {
    "@protected": true,
    level: { "@id": "https://policy.test/#level", "@nest": "wrapper" },
  };
  const root = {
    "@import": contentIdOf(shared),
    aliasLevel: {
      "@id": "https://policy.test/#level",
      "@nest": "wrapper",
    },
  };
  const harness = await buildHarness([shared, root]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "PROTECTED_ALIAS_COLLISION",
  );
});
