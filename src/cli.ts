import {
  createDeepBadCertificate,
  createMergeHarness,
  createThreadHarness,
  runForcedDeepBadMigration,
  runOriginalThreadToCompletion,
  runSafeThreadMigration,
} from "./scenarios.ts";

const asJson = process.argv.includes("--json");

const thread = createThreadHarness(3);
const safeMigrated = runSafeThreadMigration(thread);
const original = runOriginalThreadToCompletion(thread, 3);
const badCertificate = createDeepBadCertificate(thread);
const badVerification = thread.verifyCertificate(badCertificate);
const forcedBad = runForcedDeepBadMigration(thread);
const merge = createMergeHarness();

const report = {
  thread: {
    verdict: thread.verification.verdict,
    canMigrate: thread.verification.canMigrate,
    stackKinds: thread.prefixSession.stack.map((frame) => frame.kindId),
    relevantFrameIds: thread.verification.relevantFrameIds,
    recursiveInvariantFrameIds: thread.certificate.universalProof.cycleInvariantFrameIds,
    prefixBytes: thread.prefixSession.byteLength,
    prefixSha256: thread.receipt.prefixSha256,
    safeMigrationStatus: safeMigrated.status,
    originalStatus: original.status,
    migratedOutputEqualsOriginal: safeMigrated.output === original.output,
    escapedProof: {
      containsEscapedScriptTag: safeMigrated.output.includes("&lt;img src=x onerror="),
      containsRawScriptTag: safeMigrated.output.includes("<img src=x onerror="),
      containsEncodedUrlDelimiters: safeMigrated.output.includes("a%26b%3Dc%23d"),
    },
    outputs: asJson
      ? { prefix: thread.prefixSession.output, original: original.output, migrated: safeMigrated.output }
      : undefined,
  },
  deepBadMigration: {
    verdict: badVerification.verdict,
    codes: badVerification.findings.map((finding) => finding.code),
    forcedExperimentStatus: forcedBad.status,
    forcedExperimentContainsRawAttack: forcedBad.output.includes(
      `EXPERIMENT-RAW:<img src=x onerror="alert('deep')">`,
    ),
    rawSnippet: forcedBad.output.match(/EXPERIMENT-RAW:.{0,90}/)?.[0],
  },
  returnPointMerge: {
    verdict: merge.verification.verdict,
    relevantFrameIds: merge.verification.relevantFrameIds,
    oldAndMigratedOutputEqual: merge.migratedA.output === merge.originalA.output,
    branchA: merge.originalA.output,
    branchB: merge.originalB.output,
    migratedA: merge.migratedA.output,
  },
};

if (asJson) {
  console.log(JSON.stringify(report, null, 2));
} else {
  console.log("Template migration verification samples\n");
  console.log(`thread verdict: ${report.thread.verdict}`);
  console.log(`stack frames: ${report.thread.stackKinds.join(" -> ")}`);
  console.log(`recursive invariants: ${report.thread.recursiveInvariantFrameIds.join(", ")}`);
  console.log(`migrated == original host output: ${report.thread.migratedOutputEqualsOriginal}`);
  console.log(`escaped script proof: ${report.thread.escapedProof.containsEscapedScriptTag}`);
  console.log(`encoded URL proof: ${report.thread.escapedProof.containsEncodedUrlDelimiters}\n`);

  console.log(`deep bad verdict: ${report.deepBadMigration.verdict}`);
  console.log(`deep bad codes: ${report.deepBadMigration.codes.join(", ")}`);
  console.log(`forced host raw attack: ${report.deepBadMigration.forcedExperimentContainsRawAttack}`);
  console.log(`raw snippet: ${report.deepBadMigration.rawSnippet}\n`);

  console.log(`merge verdict: ${report.returnPointMerge.verdict}`);
  console.log(`merge frames: ${report.returnPointMerge.relevantFrameIds.join(", ")}`);
  console.log(`merged output equals old A: ${report.returnPointMerge.oldAndMigratedOutputEqual}`);
  console.log(`A: ${report.returnPointMerge.branchA}`);
  console.log(`B: ${report.returnPointMerge.branchB}`);
  console.log(`M: ${report.returnPointMerge.migratedA}`);
}
