import test from "node:test";
import assert from "node:assert/strict";
import {
  SANDBOX_FLAG,
  SandboxPolicyError,
  adjudicateSandboxFlags,
  normalizeSandboxFlags,
  requireSandboxApproval,
} from "../index.ts";

test("no flags produce an opaque origin without script execution", () => {
  const verdict = adjudicateSandboxFlags({
    allowScripts: false,
    allowSameOrigin: false,
  });

  assert.equal(verdict.allowed, true);
  assert.deepEqual(verdict.flags, {
    allowScripts: false,
    allowSameOrigin: false,
  });
  assert.equal(verdict.sandboxAttribute, "");
  assert.equal(verdict.origin, "opaque");
  assert.equal(verdict.executesScripts, false);
});

test("allow-scripts alone keeps the origin opaque and permits scripts", () => {
  const verdict = adjudicateSandboxFlags({
    allowScripts: true,
    allowSameOrigin: false,
  });

  assert.equal(verdict.allowed, true);
  assert.equal(verdict.sandboxAttribute, SANDBOX_FLAG.scripts);
  assert.equal(verdict.origin, "opaque");
  assert.equal(verdict.executesScripts, true);
});

test("allow-same-origin alone inherits the parent origin without scripts", () => {
  const verdict = adjudicateSandboxFlags({
    allowScripts: false,
    allowSameOrigin: true,
  });

  assert.equal(verdict.allowed, true);
  assert.equal(verdict.sandboxAttribute, SANDBOX_FLAG.sameOrigin);
  assert.equal(verdict.origin, "inherited");
  assert.equal(verdict.executesScripts, false);
});

test("allow-scripts plus allow-same-origin is rejected", () => {
  const verdict = adjudicateSandboxFlags({
    allowScripts: true,
    allowSameOrigin: true,
  });

  assert.equal(verdict.allowed, false);
  assert.equal(verdict.reason, "conflicting-origin-and-scripts");
  assert.match(verdict.message, /allow-scripts/);
  assert.match(verdict.message, /allow-same-origin/);
});

test("every script-granting approval requires an opaque origin", () => {
  const scriptOnly = adjudicateSandboxFlags({
    allowScripts: true,
    allowSameOrigin: false,
  });

  assert.equal(scriptOnly.allowed, true);
  assert.equal(scriptOnly.executesScripts, true);
  assert.equal(scriptOnly.origin, "opaque");
});

test("sandbox attribute strings are parsed before adjudication", () => {
  const scriptsOnly = adjudicateSandboxFlags("allow-scripts allow-scripts");
  assert.equal(scriptsOnly.allowed, true);
  assert.equal(scriptsOnly.origin, "opaque");
  assert.equal(scriptsOnly.executesScripts, true);

  const sameOriginOnly = adjudicateSandboxFlags(
    `  ${SANDBOX_FLAG.sameOrigin}\t`,
  );
  assert.equal(sameOriginOnly.allowed, true);
  assert.equal(sameOriginOnly.origin, "inherited");
  assert.equal(sameOriginOnly.executesScripts, false);

  const conflicting = adjudicateSandboxFlags(
    `${SANDBOX_FLAG.sameOrigin} ${SANDBOX_FLAG.scripts}`,
  );
  assert.equal(conflicting.allowed, false);
});

test("token arrays and boolean selections produce the same verdict", () => {
  const fromArray = normalizeSandboxFlags([
    SANDBOX_FLAG.sameOrigin,
    SANDBOX_FLAG.scripts,
  ]);
  const fromObject = normalizeSandboxFlags({
    allowScripts: true,
    allowSameOrigin: true,
  });
  const fromString = normalizeSandboxFlags(
    `${SANDBOX_FLAG.scripts} ${SANDBOX_FLAG.sameOrigin}`,
  );

  assert.deepEqual(fromArray, fromObject);
  assert.deepEqual(fromString, fromObject);
});

test("unknown sandbox flags fail closed", () => {
  assert.throws(
    () => adjudicateSandboxFlags("allow-scripts allow-forms"),
    TypeError,
  );
});

test("requireSandboxApproval throws a typed error for the unsafe pair", () => {
  assert.throws(
    () =>
      requireSandboxApproval({
        allowScripts: true,
        allowSameOrigin: true,
      }),
    (error: unknown) => {
      assert.ok(error instanceof SandboxPolicyError);
      assert.equal(error.name, "SandboxPolicyError");
      assert.equal(error.verdict.allowed, false);
      return true;
    },
  );
});
