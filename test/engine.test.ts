import { test } from "node:test";
import assert from "node:assert/strict";

import {
  ContextResolutionError,
  canonicalJson,
  contentIdOf,
  resolveContext,
  safeDiagnosticJson,
} from "../src/index.ts";
import { buildHarness, expectReject } from "./helpers.ts";
import type { ContextObject } from "../src/index.ts";

function expectAnyHardRejection(fn: () => unknown): {
  code: string;
  diagnostic: Record<string, unknown>;
} {
  try {
    fn();
  } catch (error) {
    const diagnostic = (error as { diagnostic?: Record<string, unknown> }).diagnostic;
    if (diagnostic === undefined || typeof diagnostic.code !== "string") {
      throw new Error(`Expected a ContextResolutionError, got ${String(error)}`);
    }
    return { code: diagnostic.code, diagnostic };
  }
  throw new Error("Expected a hard rejection but call succeeded");
}

async function resolve(contexts: readonly ContextObject[], root?: ContextObject) {
  const rootContext = root ?? contexts[0];
  if (rootContext === undefined) throw new Error("missing root context");
  const harness = await buildHarness(contexts);
  return {
    harness,
    result: resolveContext({
      rootCid: contentIdOf(rootContext),
      store: harness.store,
      allowed: harness.allowed,
    }),
  };
}

test("@propagate:false isolates vocab, base and term bindings", async () => {
  const scoped: ContextObject = {
    "@propagate": false,
    "@vocab": "https://scoped.test/#",
    "@base": "https://scoped.test/base/",
    scopedTerm: "https://scoped.test/#term",
  };
  const root: ContextObject = {
    "@vocab": "https://root.test/#",
    "@base": "https://root.test/base/",
    rootTerm: "https://root.test/#term",
    "@import": contentIdOf(scoped),
  };
  const { result } = await resolve([scoped, root], root);

  assert.equal(result.vocab, "https://root.test/#");
  assert.equal(result.base, "https://root.test/base/");
  assert.ok(result.terms.has("rootTerm"));
  assert.equal(result.terms.has("scopedTerm"), false);
  assert.deepEqual(
    result.stack.map((frame) => frame.propagate),
    [true, true],
  );
});

test("@propagate:true (default) keeps imported bindings visible", async () => {
  const imported: ContextObject = {
    "@vocab": "https://imported.test/#",
    importedTerm: "https://imported.test/#term",
  };
  const root: ContextObject = { "@import": contentIdOf(imported) };
  const { result } = await resolve([imported, root], root);
  assert.equal(result.vocab, "https://imported.test/#");
  assert.ok(result.terms.has("importedTerm"));
});

test("nested @nest alias conflict folds onto one protected field and rejects", async () => {
  const shared: ContextObject = {
    "@protected": true,
    outer: {
      "@id": "https://policy.test/#level",
      "@nest": "wrapper",
    },
  };
  const root: ContextObject = {
    "@import": contentIdOf(shared),
    inner: {
      "@id": "https://policy.test/#level",
      "@nest": "wrapper",
    },
  };
  const harness = await buildHarness([shared, root]);
  const caught = expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "PROTECTED_ALIAS_COLLISION",
  );
  const text = JSON.stringify(caught.diagnostic);
  assert.doesNotMatch(text, /undefined/);
});

test("identical alias values from another context still hard reject", async () => {
  const first: ContextObject = {
    "@protected": true,
    primary: "https://policy.test/#field",
  };
  const second: ContextObject = {
    alias: "https://policy.test/#field",
  };
  const root: ContextObject = {
    "@import": [contentIdOf(first), contentIdOf(second)],
  };
  const harness = await buildHarness([first, second, root]);
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

test("multi-level nested @nest aliases folding onto a protected field reject", async () => {
  const base: ContextObject = {
    "@protected": true,
    deep: {
      "@id": "https://policy.test/#deep",
      "@nest": "outer",
    },
  };
  const middle: ContextObject = {
    "@import": contentIdOf(base),
    parked: {
      "@id": "https://policy.test/#deep",
      "@nest": "outer",
    },
  };
  const root: ContextObject = {
    "@import": contentIdOf(middle),
  };
  const harness = await buildHarness([base, middle, root]);
  const caught = expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "PROTECTED_ALIAS_COLLISION",
  );
  assert.equal(caught.diagnostic.offendingTerm, "parked");
});

test("explicit @propagate:true behaves like the default", async () => {
  const imported: ContextObject = {
    "@propagate": true,
    "@vocab": "https://explicit.test/#",
    kept: "https://explicit.test/#kept",
  };
  const root: ContextObject = { "@import": contentIdOf(imported) };
  const { result } = await resolve([imported, root], root);
  assert.equal(result.vocab, "https://explicit.test/#");
  assert.ok(result.terms.has("kept"));
});

test("protected term redefinition with same value but different cid rejects", async () => {
  const first: ContextObject = {
    "@protected": true,
    status: { "@id": "https://policy.test/#status" },
  };
  const second: ContextObject = {
    "@import": contentIdOf(first),
    "@protected": true,
    status: { "@id": "https://policy.test/#status" },
    discriminator: "https://policy.test/#second",
  };
  const harness = await buildHarness([first, second]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(second),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "PROTECTED_TERM_REDEFINED",
  );
});

test("same content id repeated through a diamond import is idempotent", async () => {
  const leaf: ContextObject = {
    "@protected": true,
    shared: "https://policy.test/#shared",
  };
  const left: ContextObject = { "@import": contentIdOf(leaf) };
  const right: ContextObject = { "@import": contentIdOf(leaf) };
  const diamond: ContextObject = buildDiamond(left, right);
  const root: ContextObject = { "@import": contentIdOf(diamond) };
  const harness = await buildHarness([leaf, left, right, diamond, root]);
  const result = resolveContext({
    rootCid: contentIdOf(root),
    store: harness.store,
    allowed: harness.allowed,
  });
  assert.ok(result.protectedTerms.has("shared"));
  assert.deepEqual(new Set(result.closure), new Set([
    contentIdOf(root),
    contentIdOf(diamond),
    contentIdOf(left),
    contentIdOf(right),
    contentIdOf(leaf),
  ]));
});

test("diamond with divergent second branch rejects protected collision", async () => {
  const leaf: ContextObject = {
    "@protected": true,
    shared: "https://policy.test/#shared",
  };
  const left: ContextObject = { "@import": contentIdOf(leaf) };
  const right: ContextObject = {
    otherName: "https://policy.test/#shared",
  };
  const diamond: ContextObject = buildDiamond(left, right);
  const root: ContextObject = { "@import": contentIdOf(diamond) };
  const harness = await buildHarness([leaf, left, right, diamond, root]);
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

test("import cycle fails closed with complete diagnostics", async () => {
  const a: ContextObject = { "@vocab": "https://a.test/#" };
  const aCid = contentIdOf(a);
  const b: ContextObject = { "@import": aCid };
  const bCid = contentIdOf(b);
  const shadowA: ContextObject = {
    "@vocab": "https://a.test/#",
    "@import": bCid,
  };
  const harness = await buildHarness([a, b]);
  const cycleStore = new CycleDecorator(
    harness.store,
    aCid,
    shadowA,
  );
  const result = expectAnyHardRejection(
    () =>
      resolveContext({
        rootCid: bCid,
        store: cycleStore,
        allowed: harness.allowed,
      }),
  );
  assert.ok(
    result.code === "IMPORT_CYCLE_DETECTED" ||
      result.code === "CONTENT_DIGEST_MISMATCH",
  );
  assert.ok(Array.isArray(result.diagnostic.importPath));
  assert.doesNotMatch(JSON.stringify(result.diagnostic), /undefined/);
});

/**
 * Decorates the trusted store so a previously acyclic context now imports its
 * own ancestor. Digest verification still runs; cycle detection fires first
 * because ancestor tracking precedes trust checks on revisited frames.
 */
class CycleDecorator {
  readonly #delegate: Awaited<ReturnType<typeof buildHarness>>["store"];
  readonly #targetCid: string;
  readonly #shadowObject: ContextObject;

  constructor(
    delegate: Awaited<ReturnType<typeof buildHarness>>["store"],
    targetCid: string,
    shadowObject: ContextObject,
  ) {
    this.#delegate = delegate;
    this.#targetCid = targetCid;
    this.#shadowObject = shadowObject;
  }

  has(cid: string): boolean {
    return cid === this.#targetCid || this.#delegate.has(cid);
  }

  get(cid: string): ContextObject | undefined {
    return cid === this.#targetCid ? this.#shadowObject : this.#delegate.get(cid);
  }

  canonicalBytes(cid: string): Uint8Array | undefined {
    if (cid === this.#targetCid) {
      return new TextEncoder().encode(canonicalJson(this.#shadowObject));
    }
    return this.#delegate.canonicalBytes(cid);
  }

  ids(): readonly string[] {
    return this.#delegate.ids();
  }
}


/** Diamond: one frame imports both branches; both converge on a shared leaf. */
function buildDiamond(left: ContextObject, right: ContextObject): ContextObject {
  return { "@import": [contentIdOf(left), contentIdOf(right)] };
}

test("import depth is capped at four levels", async () => {
  const c4: ContextObject = { "@vocab": "https://c4.test/#" };
  const c3: ContextObject = { "@import": contentIdOf(c4) };
  const c2: ContextObject = { "@import": contentIdOf(c3) };
  const c1: ContextObject = { "@import": contentIdOf(c2) };
  const root: ContextObject = { "@import": contentIdOf(c1) };
  const harness = await buildHarness([c4, c3, c2, c1, root]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "IMPORT_DEPTH_EXCEEDED",
  );
});

test("@reverse protected forward/reverse folding is rejected", async () => {
  const forward: ContextObject = {
    "@protected": true,
    holder: "https://policy.test/#holder",
  };
  const reverse: ContextObject = {
    heldBy: { "@reverse": "https://policy.test/#holder" },
  };
  const root: ContextObject = {
    "@import": [contentIdOf(forward), contentIdOf(reverse)],
  };
  const harness = await buildHarness([forward, reverse, root]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "PROTECTED_REVERSE_COLLISION",
  );
});

test("@type @id and @vocab coercion is recorded on resolved terms", async () => {
  const root: ContextObject = {
    "@vocab": "https://v.test/#",
    "@base": "https://b.test/",
    idTerm: { "@id": "https://v.test/#idTerm", "@type": "@id" },
    vocabTerm: { "@id": "https://v.test/#vocabTerm", "@type": "@vocab" },
  };
  const { result } = await resolve([root]);
  assert.equal(result.terms.get("idTerm")?.forcedType, "@id");
  assert.equal(result.terms.get("vocabTerm")?.forcedType, "@vocab");
});

test("unsupported forced types are refused", async () => {
  const root: ContextObject = {
    bad: { "@id": "https://v.test/#bad", "@type": "https://unknown.test/#Type" },
  };
  const harness = await buildHarness([root]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "UNSUPPORTED_FORCED_TYPE",
  );
});

test("unknown keyword cannot silently rewrite protected semantics", async () => {
  const root: ContextObject = {
    "@protected": true,
    sneaky: {
      "@id": "https://v.test/#sneaky",
      "@unknownKeyword": "rewrite",
    } as unknown as ContextObject,
  };
  const harness = await buildHarness([root]);
  expectReject(
    () =>
      resolveContext({
        rootCid: contentIdOf(root),
        store: harness.store,
        allowed: harness.allowed,
      }),
    "UNKNOWN_KEYWORD_REWRITE",
  );
});

test("every thrown error exposes a fully populated diagnostic", async () => {
  const root: ContextObject = {
    "@protected": true,
    status: { "@id": "https://policy.test/#status" },
  };
  const other: ContextObject = {
    "@import": contentIdOf(root),
    "@protected": true,
    status: { "@id": "https://policy.test/#status" },
    secondMarker: "https://policy.test/#second-marker",
  };
  const harness = await buildHarness([root, other]);
  try {
    resolveContext({
      rootCid: contentIdOf(other),
      store: harness.store,
      allowed: harness.allowed,
    });
    assert.fail("expected collision");
  } catch (error) {
    assert.ok(error instanceof ContextResolutionError);
    const parsed = JSON.parse(safeDiagnosticJson(error.diagnostic)) as Record<
      string,
      unknown
    >;
    assert.equal(typeof parsed.code, "string");
    assert.equal(typeof parsed.message, "string");
    assert.equal(typeof parsed.offendingTerm, "string");
    assert.equal(typeof parsed.offendingCid, "string");
    assert.ok(Array.isArray(parsed.importPath));
    assert.equal(typeof parsed.depth, "number");
    assert.equal(typeof parsed.detail, "string");
  }
});
