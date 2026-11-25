import assert from "node:assert/strict";
import { test } from "node:test";

import {
  createDeepBadCertificate,
  createMergeHarness,
  createThreadHarness,
  runForcedDeepBadMigration,
  runOriginalThreadToCompletion,
  runSafeThreadMigration,
} from "../src/scenarios.ts";
import { renderToCompletion } from "../src/host.ts";
import { verifyMigration } from "../src/certificate.ts";
import { verifyEncoderBinding } from "../src/encoders.ts";
import { validateStackSummary } from "../src/stack.ts";
import { issueCertificate } from "../src/certificate.ts";

test("thread certificate is safe and migrated output matches old host", () => {
  const harness = createThreadHarness(3);
  assert.equal(harness.verification.verdict, "safe");
  assert.deepEqual(harness.verification.findings, []);
  assert.equal(harness.prefixSession.stack.length, 4);
  assert.ok(
    harness.verification.relevantFrameIds.includes("commentItem:ci-after-replies"),
    "deep recursive return frame must be covered",
  );
  assert.ok(
    harness.certificate.universalProof.cycleInvariantFrameIds.includes(
      "commentItem:ci-after-replies",
    ),
  );

  const portableCertificate = JSON.parse(JSON.stringify(harness.certificate)) as typeof harness.certificate;
  const reverified = harness.verifyCertificate(portableCertificate);
  assert.equal(reverified.verdict, "safe");

  const migrated = runSafeThreadMigration(harness);
  assert.equal(migrated.status, "complete");

  const oldFull = renderToCompletion({
    template: harness.oldTemplate.value,
    encoders: harness.oldEncoders.value,
    data: harness.data,
  });
  assert.equal(oldFull.status, "complete");
  assert.equal(migrated.output, oldFull.output);
});

test("malformed verification input is isolated instead of throwing", () => {
  const result = verifyMigration(undefined as never);
  assert.equal(result.verdict, "quarantined");
  assert.equal(result.canMigrate, false);
  assert.ok(result.findings.some((finding) => finding.code === "VERIFIER_EXCEPTION"));
});

test("published prefix preserves escaped source and cross-encoder output", () => {
  const harness = createThreadHarness(3);
  assert.match(harness.prefixSession.output, /&lt;img src=x onerror=/);
  assert.doesNotMatch(harness.prefixSession.output, /<img src=x onerror=/);
  assert.match(harness.prefixSession.output, /a%26b%3Dc%23d%20e%25f%22/);
  assert.equal(harness.receipt.byteLength, Buffer.byteLength(harness.prefixSession.output));
  assert.ok(harness.authenticator.verify(harness.receipt));
});

test("original old template can safely complete without migration", () => {
  const harness = createThreadHarness(2);
  const original = runOriginalThreadToCompletion(harness, 2);
  assert.equal(original.status, "complete");
  assert.match(original.output, /&lt;img src=x onerror=/);
  assert.doesNotMatch(original.output, /<img src=x onerror=/);
  assert.match(original.output, /&lt;edited&gt; &amp; safe/);
  assert.match(original.output, /<\/html>$/);
});

test("deep-only raw continuation is denied by stack certificate", () => {
  const harness = createThreadHarness(3);
  const badCertificate = createDeepBadCertificate(harness);
  const result = harness.verifyCertificate(badCertificate);

  assert.equal(result.verdict, "denied");
  const codes = result.findings.map((finding) => finding.code);
  assert.ok(codes.includes("ENCODER_RENAME_CONTEXT_MISMATCH"));
  assert.ok(codes.includes("FRAME_CONTEXT_RENAMED"));
  assert.ok(codes.includes("NEW_TAIL_CLOSE_OBLIGATION_UNMET"));
  assert.ok(
    codes.includes("TAIL_FIELD_SECURITY_CONTEXT_CHANGED") ||
      codes.includes("TAIL_BINDING_MISSING_IN_NEW_VERSION"),
  );

  const forced = runForcedDeepBadMigration(harness);
  assert.ok(
    forced.output.includes(`EXPERIMENT-RAW:<img src=x onerror="alert('deep')">`),
    "forced bad continuation must be observably unsafe in the real host",
  );
});

test("missing deep continuation quarantines instead of allowing entry status", () => {
  const harness = createThreadHarness(3);
  const incompleteCertificate = structuredClone(harness.certificate);
  delete incompleteCertificate.continuations["commentItem:ci-after-replies"];

  const result = harness.verifyCertificate(incompleteCertificate);
  assert.equal(result.verdict, "quarantined");
  assert.equal(result.canMigrate, false);
  assert.ok(result.findings.some((f) => f.code === "CERT_FRAME_CONTINUATION_MISSING"));
});

test("matching entry completion statuses cannot authorize a deep bad migration", () => {
  const harness = createThreadHarness(3);
  const oldEntry = renderToCompletion({
    template: harness.oldTemplate.value,
    encoders: harness.oldEncoders.value,
    data: harness.data,
  });
  const newEntry = renderToCompletion({
    template: harness.newTemplate.value,
    encoders: harness.newEncoders.value,
    data: harness.data,
  });

  assert.equal(oldEntry.status, "complete");
  assert.equal(newEntry.status, "complete");
  assert.equal(harness.verifyCertificate(createDeepBadCertificate(harness)).verdict, "denied");
});

test("incomplete recursive stack summary is isolated", () => {
  const harness = createThreadHarness(3);
  const incompleteSummary = structuredClone(harness.summary);
  incompleteSummary.complete = false;
  incompleteSummary.incompletenessReasons.push("native async frame omitted by embedder");

  const directFindings = validateStackSummary(incompleteSummary, harness.oldTemplate.value);
  assert.ok(directFindings.some((f) => f.code === "STACK_SUMMARY_INCOMPLETE"));

  const { auth: _auth, ...receiptDraft } = harness.receipt;
  delete receiptDraft.stackSummarySha256;
  const unboundReceipt = harness.authenticator.authenticate(receiptDraft);

  const certificate = issueCertificate({
    certificateId: "cert-incomplete-summary",
    boundaryId: harness.certificate.boundaryId,
    alignmentKey: harness.certificate.alignmentKey,
    oldTemplate: harness.oldTemplate,
    newTemplate: harness.newTemplate,
    oldEncoders: harness.oldEncoders,
    newEncoders: harness.newEncoders,
    stackSummary: incompleteSummary,
    receipt: unboundReceipt,
    continuations: harness.safeContinuations,
    encoderBindings: harness.encoderBindings,
  });

  const result = verifyMigration({
    certificate,
    oldTemplate: harness.oldTemplate,
    newTemplate: harness.newTemplate,
    oldEncoders: harness.oldEncoders,
    newEncoders: harness.newEncoders,
    stackSummary: incompleteSummary,
    receipt: unboundReceipt,
    prefix: harness.prefixSession.output,
    authenticator: harness.authenticator,
  });

  assert.equal(result.verdict, "quarantined");
  assert.ok(result.findings.some((f) => f.code === "STACK_SUMMARY_INCOMPLETE"));
  assert.ok(result.findings.some((f) => f.code === "RECEIPT_STACK_BINDING_MISSING"));
});

test("receipt without verifier key is quarantined", () => {
  const harness = createThreadHarness(3);
  const result = verifyMigration({
    certificate: harness.certificate,
    oldTemplate: harness.oldTemplate,
    newTemplate: harness.newTemplate,
    oldEncoders: harness.oldEncoders,
    newEncoders: harness.newEncoders,
    stackSummary: harness.summary,
    receipt: harness.receipt,
    prefix: harness.prefixSession.output,
  });

  assert.equal(result.verdict, "quarantined");
  assert.ok(result.findings.some((f) => f.code === "RECEIPT_VERIFIER_KEY_MISSING"));
});

test("tampered receipt is denied", () => {
  const harness = createThreadHarness(3);
  const tampered = structuredClone(harness.receipt);
  tampered.byteLength += 1;

  const result = verifyMigration({
    certificate: harness.certificate,
    oldTemplate: harness.oldTemplate,
    newTemplate: harness.newTemplate,
    oldEncoders: harness.oldEncoders,
    newEncoders: harness.newEncoders,
    stackSummary: harness.summary,
    receipt: tampered,
    authenticator: harness.authenticator,
  });

  assert.equal(result.verdict, "denied");
  assert.ok(result.findings.some((f) => f.code === "RECEIPT_MAC_INVALID"));
  assert.ok(result.findings.some((f) => f.code === "CERT_INPUT_HASH_MISMATCH"));
});

test("boundary mismatch is denied", () => {
  const harness = createThreadHarness(3);
  const badCertificate = structuredClone(harness.certificate);
  badCertificate.boundaryId = "some-later-output-boundary";
  const result = harness.verifyCertificate(badCertificate);
  assert.equal(result.verdict, "denied");
  assert.ok(result.findings.some((f) => f.code === "MIGRATION_BOUNDARY_NOT_ALIGNED"));
});

test("encoder state rename across contexts is rejected independently", () => {
  const harness = createThreadHarness(3);
  const findings = verifyEncoderBinding(
    {
      oldEncoderId: "html",
      oldStateId: "text",
      newEncoderId: "html",
      newStateId: "doubleQuotedAttribute",
    },
    harness.oldEncoders.value,
    harness.newEncoders.value,
  );
  assert.ok(findings.some((f) => f.code === "ENCODER_RENAME_CONTEXT_MISMATCH"));
  assert.ok(findings.some((f) => f.code === "ENCODER_RENAME_SEMANTIC_MISMATCH"));
});

test("shared fragment and merged return points preserve real output", () => {
  const merge = createMergeHarness();
  assert.equal(merge.verification.verdict, "safe");
  assert.deepEqual(merge.verification.findings, []);
  assert.ok(merge.summary.rootFrameIds?.includes("branchB:branch-b-rp"));
  assert.ok(
    merge.verification.relevantFrameIds.includes("branchA:branch-a-rp") &&
      merge.verification.relevantFrameIds.includes("branchB:branch-b-rp"),
  );
  assert.equal(merge.migratedA.status, "complete");
  assert.equal(merge.migratedA.output, merge.originalA.output);
  assert.match(merge.originalA.output, /&lt;merge &amp; "x"&gt;/);
  assert.match(merge.originalB.output, /data-branch="B"/);
  assert.match(merge.originalB.output, /&lt;merge &amp; "x"&gt;/);
});
