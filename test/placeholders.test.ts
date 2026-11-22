import assert from "node:assert/strict";
import test from "node:test";
import {
  PipelineError,
  computePlaceholderBinding,
  createPipeline,
  normalizeManifest,
  replacePlaceholders,
} from "../src/index.ts";
import { fixture, text, utf8 } from "./helpers.ts";

test("only the manifest-declared placeholder span is replaced", async () => {
  const raw = text("A__REQ__B__REQ__C");
  const token = text("__REQ__");
  const offset = utf8(raw).indexOf("__REQ__");
  assert.equal(offset, 1);
  const { manifest, transport } = fixture({
    decoded: raw,
    placeholders: [
      {
        id: "req",
        offset,
        length: token.byteLength,
        token,
        field: "requestId",
      },
    ],
  });

  const pipeline = createPipeline(manifest, {
    identity: { requestId: "alice" },
  });
  pipeline.addShard(0, transport);
  const asset = await pipeline.end();
  assert.equal(utf8(asset.bytes), "AaliceB__REQ__C");
  assert.equal(asset.replacements.length, 1);
  assert.equal(asset.replacements[0].bindingVerified, true);
});

test("direct replacement is single-pass and positional", () => {
  const raw = text("x__TOKEN__y__TOKEN__z");
  const token = text("__TOKEN__");
  const { manifest } = fixture({
    decoded: raw,
    placeholders: [
      {
        id: "token",
        offset: 1,
        length: token.byteLength,
        token,
        value: "VALUE",
        binding: "none",
      },
    ],
  });
  const result = replacePlaceholders(raw, manifest.placeholders, {});
  assert.equal(utf8(result.bytes), "xVALUEy__TOKEN__z");
});

test("a replacement bound to another request identity is rejected", async () => {
  const raw = text("xxSLOTxx");
  const token = text("SLOT");
  const identityA = {
    requestId: "request-a",
    body: "body",
    responseType: "text",
    placeholderValues: { slot: "secret" },
  };
  const bindingSpec = {
    bindingAlgorithm: "sha256" as const,
    bindingDomain: "gsb.placeholder.slot",
  };
  const bindingDigest = computePlaceholderBinding(
    identityA,
    bindingSpec,
    "secret",
  );
  const placeholder = {
    id: "slot",
    offset: 2,
    length: token.byteLength,
    token,
    binding: "identity" as const,
    ...bindingSpec,
    bindingDigest,
  };
  const { manifest, transport } = fixture({
    decoded: raw,
    placeholders: [placeholder],
  });

  const accepted = createPipeline(manifest, { identity: identityA });
  accepted.addShard(0, transport);
  const asset = await accepted.end();
  assert.equal(utf8(asset.bytes), "xxsecretxx");

  const rejected = createPipeline(manifest, {
    identity: {
      requestId: "request-b",
      body: "body",
      responseType: "text",
      placeholderValues: { slot: "secret" },
    },
  });
  rejected.addShard(0, transport);
  await assert.rejects(
    rejected.end(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "PLACEHOLDER_BINDING",
  );
});

test("placeholder spans must be declared, in range, and non-overlapping", () => {
  const bytes = text("0123456789");
  assert.throws(
    () =>
      normalizeManifest({
        transport: {
          domain: "transport",
          length: bytes.byteLength,
          algorithm: "sha256",
          digest: "00",
        },
        decoded: {
          domain: "decoded",
          length: bytes.byteLength,
          algorithm: "sha256",
          digest: "00",
        },
        placeholders: [
          { id: "a", offset: 0, length: 5 },
          { id: "b", offset: 4, length: 2 },
        ],
      }),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "PLACEHOLDER_RANGE",
  );
});

test("decodedStage=final verifies the post-replacement bytes", async () => {
  const raw = text("hello __WHO__!");
  const token = text("__WHO__");
  const finalBytes = text("hello world!");
  const { manifest, transport } = fixture({
    decoded: raw,
    finalBytes,
    decodedStage: "final",
    placeholders: [
      {
        id: "who",
        offset: 6,
        length: token.byteLength,
        token,
        field: "requestId",
      },
    ],
  });
  const pipeline = createPipeline(manifest, {
    identity: { requestId: "world" },
  });
  pipeline.addShard(0, transport);
  const asset = await pipeline.end();
  assert.equal(utf8(asset.bytes), "hello world!");
  assert.equal(asset.decodedStage, "final");
});
