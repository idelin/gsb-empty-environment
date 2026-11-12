import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  ContextPolicyError,
  ContextRegistry,
  VC_BASE_CONTEXT,
  computeDigest,
  createDocumentLoader,
  validateCredentialContexts,
} from "./context-policy.ts";

const ATTACKER_CONTEXT_URL = "https://attacker.example/evil-context.jsonld";
const EXTENSION_CONTEXT_URL = "https://example.com/ext/v1";

const baseContextDocument = {
  "@context": {
    "@version": 1.1,
    "@protected": true,
    id: "@id",
    type: "@type",
    VerifiableCredential: {
      "@id": "https://www.w3.org/2018/credentials#VerifiableCredential",
      "@protected": true,
    },
    issuer: {
      "@id": "https://www.w3.org/2018/credentials#issuer",
      "@type": "@id",
      "@protected": true,
    },
  },
};

const extensionContextDocument = {
  "@context": {
    CoffeeCredential: "https://example.com/ext#CoffeeCredential",
    roastLevel: "https://example.com/ext#roastLevel",
  },
};

function makeRegistry(): ContextRegistry {
  const registry = new ContextRegistry();
  registry.register(VC_BASE_CONTEXT, baseContextDocument);
  registry.register(EXTENSION_CONTEXT_URL, extensionContextDocument);
  return registry;
}

function makeCredential(context: unknown[]): Record<string, unknown> {
  return {
    "@context": context,
    type: ["VerifiableCredential"],
    issuer: "did:example:issuer",
  };
}

describe("未登记 URL", () => {
  it("拒绝未登记的远程 context，且不发起网络请求", () => {
    const registry = makeRegistry();
    const credential = makeCredential([VC_BASE_CONTEXT, ATTACKER_CONTEXT_URL]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) => {
        assert.ok(error instanceof ContextPolicyError);
        assert.equal(error.code, "UNREGISTERED_CONTEXT");
        return true;
      },
    );
  });

  it("documentLoader 对未登记 URL 抛错（无网络回退）", async () => {
    const loader = createDocumentLoader(makeRegistry());
    await assert.rejects(loader(ATTACKER_CONTEXT_URL), (error: unknown) => {
      assert.ok(error instanceof ContextPolicyError);
      assert.equal((error as ContextPolicyError).code, "UNREGISTERED_CONTEXT");
      return true;
    });
  });

  it("documentLoader 返回已登记 context 的内容", async () => {
    const loader = createDocumentLoader(makeRegistry());
    const result = await loader(VC_BASE_CONTEXT);
    assert.deepEqual(result.document, baseContextDocument);
    assert.equal(result.documentUrl, VC_BASE_CONTEXT);
  });

  it("登记内容与摘要不符时拒绝", () => {
    const registry = new ContextRegistry();
    assert.throws(
      () =>
        registry.register(ATTACKER_CONTEXT_URL, { "@context": {} }, "0".repeat(64)),
      (error: unknown) =>
        error instanceof ContextPolicyError && error.code === "DIGEST_MISMATCH",
    );
  });
});

describe("受保护术语重定义", () => {
  it("后续远程 context 不得重定义 protected 术语", () => {
    const registry = new ContextRegistry();
    registry.register(VC_BASE_CONTEXT, baseContextDocument);
    registry.register(ATTACKER_CONTEXT_URL, {
      "@context": {
        issuer: { "@id": "https://attacker.example#issuer" },
      },
    });
    const credential = makeCredential([VC_BASE_CONTEXT, ATTACKER_CONTEXT_URL]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "PROTECTED_TERM_REDEFINITION",
    );
  });

  it("凭证内联 context 不得重定义 protected 术语", () => {
    const registry = makeRegistry();
    const credential = makeCredential([
      VC_BASE_CONTEXT,
      {
        issuer: { "@id": "https://attacker.example#issuer" },
      },
    ]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "PROTECTED_TERM_REDEFINITION",
    );
  });

  it("内联 context 声明的 protected 术语同样受后续保护", () => {
    const registry = makeRegistry();
    const credential = makeCredential([
      VC_BASE_CONTEXT,
      { roastLevel: { "@id": "https://example.com/ext#roastLevel", "@protected": true } },
      { roastLevel: { "@id": "https://attacker.example#roastLevel" } },
    ]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "PROTECTED_TERM_REDEFINITION",
    );
  });

  it("未受保护的术语允许后续扩展，合法凭证通过", () => {
    const registry = makeRegistry();
    const credential = makeCredential([
      VC_BASE_CONTEXT,
      EXTENSION_CONTEXT_URL,
      { extraTerm: "https://example.com/ext#extra" },
    ]);
    assert.doesNotThrow(() => validateCredentialContexts(credential, registry));
  });
});

describe("上下文顺序", () => {
  it("@context 首元素必须是基础凭证上下文", () => {
    const registry = makeRegistry();
    const credential = makeCredential([EXTENSION_CONTEXT_URL, VC_BASE_CONTEXT]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "MISSING_BASE_CONTEXT",
    );
  });

  it("攻击者 context 置顶时拒绝", () => {
    const registry = makeRegistry();
    const credential = makeCredential([ATTACKER_CONTEXT_URL, VC_BASE_CONTEXT]);
    assert.throws(
      () => validateCredentialContexts(credential, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "MISSING_BASE_CONTEXT",
    );
  });

  it("@context 不是非空数组时拒绝", () => {
    const registry = makeRegistry();
    assert.throws(
      () => validateCredentialContexts({ "@context": VC_BASE_CONTEXT }, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "MISSING_BASE_CONTEXT",
    );
    assert.throws(
      () => validateCredentialContexts({ "@context": [] }, registry),
      (error: unknown) =>
        error instanceof ContextPolicyError &&
        error.code === "MISSING_BASE_CONTEXT",
    );
  });

  it("仅基础上下文的凭证通过", () => {
    const registry = makeRegistry();
    assert.doesNotThrow(() =>
      validateCredentialContexts(makeCredential([VC_BASE_CONTEXT]), registry),
    );
  });
});

describe("摘要", () => {
  it("computeDigest 对键序不敏感", () => {
    const a = { x: 1, y: [1, { b: 2, a: 1 }] };
    const b = { y: [1, { a: 1, b: 2 }], x: 1 };
    assert.equal(computeDigest(a), computeDigest(b));
  });
});
