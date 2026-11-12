import { test } from "node:test";
import assert from "node:assert/strict";
import {
  AdmissionGate,
  createDefaultMatrix,
  parseOrigin,
} from "../index.ts";

const matrix = createDefaultMatrix();
const gate = new AdmissionGate(matrix);
const doc = parseOrigin("https://app.example.com")!;

test("不完备矩阵在构造门时被拒绝", () => {
  const broken = { ...matrix } as Record<string, string>;
  delete broken[Object.keys(broken)[0]];
  assert.throws(() => new AdmissionGate(broken), /策略矩阵/);
});

test("非法资源 URL 直接拒绝", () => {
  const decision = gate.evaluate({
    documentOrigin: doc,
    resourceUrl: "javascript:alert(1)",
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    declaration: { corp: "none", corsAccess: false },
  });
  assert.equal(decision.verdict, "deny");
  assert.equal(decision.reason, "invalid-origin");
});

test("require-corp 下无 CORP 的跨源 no-cors 被拒，加 CORP 后获许", () => {
  const base = {
    documentOrigin: doc,
    resourceUrl: "https://cdn.third-party.org/img.png",
    resourceMode: "no-cors" as const,
    credentials: "omit" as const,
    embedding: "require-corp" as const,
  };
  assert.equal(
    gate.evaluate({ ...base, declaration: { corp: "none", corsAccess: false } })
      .verdict,
    "deny",
  );
  assert.equal(
    gate.evaluate({
      ...base,
      declaration: { corp: "cross-origin", corsAccess: false },
    }).verdict,
    "allow",
  );
});

test("重定向链：每跳重新计算来源关系，第二跳被拒则整体拒绝", () => {
  const decision = gate.evaluateRedirectChain({
    documentOrigin: doc,
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    hops: [
      {
        resourceUrl: "https://cdn.example.com/redirect",
        declaration: { corp: "same-site", corsAccess: false },
      },
      {
        resourceUrl: "https://evil.example.org/final",
        declaration: { corp: "none", corsAccess: false },
      },
    ],
  });
  assert.equal(decision.verdict, "deny");
  assert.equal(decision.evaluations.length, 2);
  assert.equal(decision.evaluations[0].verdict, "allow");
  assert.equal(decision.evaluations[0].key.relationship, "same-site");
  assert.equal(decision.evaluations[1].verdict, "deny");
  assert.equal(decision.evaluations[1].key.relationship, "cross-site");
});

test("重定向链：全部跳获许才整体获许", () => {
  const decision = gate.evaluateRedirectChain({
    documentOrigin: doc,
    resourceMode: "no-cors",
    credentials: "omit",
    embedding: "require-corp",
    hops: [
      {
        resourceUrl: "https://cdn.example.com/redirect",
        declaration: { corp: "same-site", corsAccess: false },
      },
      {
        resourceUrl: "https://assets.third-party.org/final",
        declaration: { corp: "cross-origin", corsAccess: false },
      },
    ],
  });
  assert.equal(decision.verdict, "allow");
  assert.equal(decision.evaluations.length, 2);
});

test("空重定向链抛错", () => {
  assert.throws(() =>
    gate.evaluateRedirectChain({
      documentOrigin: doc,
      resourceMode: "no-cors",
      credentials: "omit",
      embedding: "unsafe-none",
      hops: [],
    }),
  );
});
