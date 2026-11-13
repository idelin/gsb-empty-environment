import test from "node:test";
import assert from "node:assert/strict";
import {
  DEFAULT_SRCDOC_HTML,
  SRCDOC_VARIANTS,
  SandboxPolicyError,
  buildSrcdocDocument,
  buildSrcdocDocuments,
  buildSrcdocMatrix,
  renderSrcdocIframe,
} from "../index.ts";

test("the matrix builds one attempt for each of the four flag combinations", () => {
  const attempts = buildSrcdocMatrix();

  assert.equal(attempts.length, 4);
  assert.equal(attempts.length, SRCDOC_VARIANTS.length);
  assert.deepEqual(
    attempts.map((attempt) => attempt.flags),
    SRCDOC_VARIANTS.map((variant) => variant.flags),
  );
  assert.equal(new Set(attempts.map((attempt) => attempt.id)).size, 4);
  assert.equal(
    attempts.filter((attempt) => attempt.document !== null).length,
    3,
  );
  assert.equal(
    attempts.filter((attempt) => attempt.document === null).length,
    1,
  );
});

test("each accepted document carries the sandbox attribute for its flags", () => {
  const attempts = buildSrcdocMatrix();
  const byId = new Map(attempts.map((attempt) => [attempt.id, attempt]));

  const noFlags = byId.get("no-flags");
  assert.ok(noFlags?.document);
  assert.equal(noFlags.document.sandboxAttribute, "");
  assert.equal(noFlags.document.origin, "opaque");
  assert.equal(noFlags.document.executesScripts, false);

  const scriptsOnly = byId.get("scripts-only");
  assert.ok(scriptsOnly?.document);
  assert.equal(scriptsOnly.document.sandboxAttribute, "allow-scripts");
  assert.equal(scriptsOnly.document.origin, "opaque");
  assert.equal(scriptsOnly.document.executesScripts, true);

  const sameOriginOnly = byId.get("same-origin-only");
  assert.ok(sameOriginOnly?.document);
  assert.equal(
    sameOriginOnly.document.sandboxAttribute,
    "allow-same-origin",
  );
  assert.equal(sameOriginOnly.document.origin, "inherited");
  assert.equal(sameOriginOnly.document.executesScripts, false);
});

test("the conflicting pair is rejected without constructing a srcdoc", () => {
  const attempt = buildSrcdocMatrix().find(
    (candidate) => candidate.id === "scripts-and-same-origin",
  );

  assert.ok(attempt);
  assert.equal(attempt.verdict.allowed, false);
  assert.equal(attempt.document, null);
});

test("direct construction of the conflicting pair throws", () => {
  assert.throws(
    () =>
      buildSrcdocDocument({
        allowScripts: true,
        allowSameOrigin: true,
      }),
    SandboxPolicyError,
  );
});

test("every document is created once and uses the supplied html", () => {
  const html = "<!doctype html><script>probe()</script>";
  const documents = buildSrcdocDocuments({ html });

  assert.equal(documents.length, 3);
  for (const document of documents) {
    assert.equal(document.srcdoc, html);
  }

  const matrix = buildSrcdocMatrix({ html });
  const built = matrix
    .map((attempt) => attempt.document)
    .filter((document) => document !== null);
  assert.equal(built.length, 3);
  assert.equal(new Set(built).size, 3);
});

test("documents default to the script probe content", () => {
  const documents = buildSrcdocDocuments();
  for (const document of documents) {
    assert.equal(document.srcdoc, DEFAULT_SRCDOC_HTML);
    assert.match(document.srcdoc, /parent\.postMessage/);
  }
});

test("renderSrcdocIframe always emits a sandbox attribute", () => {
  const documents = buildSrcdocDocuments();
  const markup = documents.map(renderSrcdocIframe);

  assert.equal(markup.length, 3);
  assert.match(markup[0], /^<iframe sandbox="" srcdoc="/);
  assert.match(markup[1], /^<iframe sandbox="allow-scripts" srcdoc="/);
  assert.match(markup[2], /^<iframe sandbox="allow-same-origin" srcdoc="/);

  for (const iframe of markup) {
    assert.match(iframe, /\bsandbox="/);
    assert.match(iframe, /referrerpolicy="no-referrer"/);
  }
});
