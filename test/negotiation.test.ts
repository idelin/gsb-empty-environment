import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  computeDeclarationDigest,
  createDeclaration,
  negotiate,
  NegotiationError,
  solveConfiguration,
  TranscriptValidator,
  verifyDeclarationIntegrity,
} from "../src/index.ts";
import type { CapabilityOffer, Declaration } from "../src/index.ts";

const FIXED_TIME = 1_760_000_000_000;

function decl(party: string, round: string, caps: CapabilityOffer[]): Declaration {
  return createDeclaration(party, round, caps, FIXED_TIME);
}

describe("configuration solver", () => {
  it("picks the highest-priority mutex branch and pulls in dependencies", () => {
    const a = decl("A", "r1", [
      { name: "h265", priority: 4, requires: ["hwdec"], conflictsWith: ["h264"] },
      { name: "hwdec", priority: 1 },
      { name: "h264", priority: 3 },
      { name: "hdr", priority: -1 },
      { name: "fec", priority: 1 },
    ]);
    const b = decl("B", "r1", [
      { name: "h265", priority: 4, requires: ["hwdec"] },
      { name: "hwdec", priority: 1 },
      { name: "h264", priority: 3 },
      { name: "hdr", priority: -1 },
      { name: "fec", priority: 1 },
    ]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "AGREED");
    if (outcome.status !== "AGREED") return;
    assert.deepEqual(outcome.config, ["fec", "h265", "hwdec"]);
    assert.equal(outcome.score, 2 + 8 + 2);
  });

  it("only selects capabilities offered by both parties", () => {
    const a = decl("A", "r1", [
      { name: "shared", priority: 5 },
      { name: "only-a", priority: 100 },
    ]);
    const b = decl("B", "r1", [
      { name: "shared", priority: 5 },
      { name: "only-b", priority: 100 },
    ]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "AGREED");
    if (outcome.status !== "AGREED") return;
    assert.deepEqual(outcome.config, ["shared"]);
    assert.equal(outcome.score, 10);
  });

  it("resolves transitive dependency chains across declarations", () => {
    const a = decl("A", "r1", [
      { name: "app", priority: 5, requires: ["mid"], mandatory: true },
      { name: "mid", priority: 1 },
      { name: "core", priority: 1 },
    ]);
    const b = decl("B", "r1", [
      { name: "app", priority: 5 },
      { name: "mid", priority: 1, requires: ["core"] },
      { name: "core", priority: 1 },
    ]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "AGREED");
    if (outcome.status !== "AGREED") return;
    assert.deepEqual(outcome.config, ["app", "core", "mid"]);
  });

  it("returns a minimal conflict set when mandatory capabilities are mutually exclusive", () => {
    const a = decl("A", "r1", [
      { name: "x", priority: 1, mandatory: true, conflictsWith: ["y"] },
      { name: "y", priority: 1, mandatory: true },
      { name: "z", priority: 1, mandatory: true },
    ]);
    const b = decl("B", "r1", [
      { name: "x", priority: 1 },
      { name: "y", priority: 1 },
      { name: "z", priority: 1 },
    ]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "CONFLICT");
    if (outcome.status !== "CONFLICT") return;
    const names = outcome.conflict.requirements.map((r) => r.capability).sort();
    assert.deepEqual(names, ["x", "y"]);
    assert.deepEqual(outcome.conflict.reasons, [
      { kind: "mutual-exclusion", capabilities: ["x", "y"] },
    ]);
  });

  it("reports missing peer offers with the dependency path", () => {
    const a = decl("A", "r1", [
      { name: "auth", priority: 5, requires: ["oauth2"], mandatory: true },
      { name: "oauth2", priority: 1 },
    ]);
    const b = decl("B", "r1", [{ name: "auth", priority: 5 }]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "CONFLICT");
    if (outcome.status !== "CONFLICT") return;
    assert.deepEqual(outcome.conflict.requirements, [
      { party: "A", capability: "auth" },
    ]);
    assert.deepEqual(outcome.conflict.reasons, [
      {
        kind: "missing-offer",
        capability: "oauth2",
        offeredBy: ["A"],
        requiredBy: "A",
        path: ["auth", "oauth2"],
      },
    ]);
  });

  it("keeps conflict cores minimal when both parties mandate the same broken capability", () => {
    const a = decl("A", "r1", [
      { name: "m", priority: 1, mandatory: true, requires: ["gone"] },
    ]);
    const b = decl("B", "r1", [{ name: "m", priority: 1, mandatory: true }]);
    const outcome = solveConfiguration(a, b);
    assert.equal(outcome.status, "CONFLICT");
    if (outcome.status !== "CONFLICT") return;
    assert.equal(outcome.conflict.requirements.length, 1);
    const survivor = outcome.conflict.requirements[0]!;
    assert.equal(survivor.capability, "m");
    const reason = outcome.conflict.reasons[0]!;
    assert.equal(reason.kind, "missing-offer");
    if (reason.kind !== "missing-offer") return;
    assert.equal(reason.capability, "gone");
    assert.deepEqual(reason.offeredBy, []);
    assert.equal(reason.requiredBy, survivor.party);
    assert.deepEqual(reason.path, ["m", "gone"]);
  });
});

describe("negotiation round", () => {
  const capsA: CapabilityOffer[] = [
    { name: "video", priority: 5, mandatory: true },
    { name: "audio", priority: 3 },
  ];
  const capsB: CapabilityOffer[] = [
    { name: "video", priority: 5 },
    { name: "audio", priority: 3 },
  ];

  it("produces a transcript bound to both declarations", () => {
    const a = decl("A", "round-1", capsA);
    const b = decl("B", "round-1", capsB);
    const result = negotiate(a, b);
    assert.equal(result.status, "AGREED");
    assert.equal(result.round, "round-1");
    assert.deepEqual(result.config, ["audio", "video"]);
    assert.equal(result.declarationDigests.A, a.digest);
    assert.equal(result.declarationDigests.B, b.digest);
    assert.match(result.transcriptHash, /^[0-9a-f]{64}$/);
  });

  it("refuses to mix declarations from different rounds", () => {
    const a = decl("A", "round-2", capsA);
    const staleB = decl("B", "round-1", capsB);
    assert.throws(
      () => negotiate(a, staleB),
      (error: unknown) =>
        error instanceof NegotiationError && error.code === "ROUND_MISMATCH",
    );
  });

  it("rejects tampered declarations", () => {
    const a = decl("A", "round-1", capsA);
    const b = decl("B", "round-1", capsB);
    const forged: Declaration = {
      ...b,
      capabilities: [...b.capabilities, { name: "smuggled", priority: 99 }],
    };
    assert.equal(verifyDeclarationIntegrity(forged), false);
    assert.throws(
      () => negotiate(a, forged),
      (error: unknown) =>
        error instanceof NegotiationError &&
        error.code === "DECLARATION_TAMPERED",
    );
  });
});

describe("transcript validation", () => {
  const capsA: CapabilityOffer[] = [
    { name: "video", priority: 5, mandatory: true },
    { name: "audio", priority: 3 },
  ];
  const capsB: CapabilityOffer[] = [
    { name: "video", priority: 5 },
    { name: "audio", priority: 3 },
  ];

  function freshRound(round: string) {
    const a = decl("A", round, capsA);
    const b = decl("B", round, capsB);
    return { a, b, result: negotiate(a, b) };
  }

  it("accepts a well-formed transcript", () => {
    const validator = new TranscriptValidator();
    const { a, b, result } = freshRound("round-1");
    const report = validator.validate(result, a, b);
    assert.deepEqual(report.issues, []);
    assert.equal(report.ok, true);
  });

  it("accepts re-validation of the same transcript within its own round", () => {
    const validator = new TranscriptValidator();
    const { a, b, result } = freshRound("round-1");
    assert.equal(validator.validate(result, a, b).ok, true);
    assert.equal(validator.validate(result, a, b).ok, true);
  });

  it("detects a declaration replayed from an older round", () => {
    const validator = new TranscriptValidator();
    const first = freshRound("round-1");
    assert.equal(validator.validate(first.result, first.a, first.b).ok, true);

    const replayedB: Declaration = {
      ...decl("B", "round-2", capsB),
      nonce: first.b.nonce,
    };
    const { digest: ignored, ...replayedBody } = replayedB;
    void ignored;
    replayedB.digest = computeDeclarationDigest(replayedBody);

    const a2 = decl("A", "round-2", capsA);
    const forgedResult = negotiate(a2, replayedB);
    const report = validator.validate(forgedResult, a2, replayedB);
    assert.equal(report.ok, false);
    assert.ok(
      report.issues.some(
        (issue) => issue.code === "REPLAY_DETECTED" && issue.party === "B",
      ),
    );
  });

  it("flags declarations from a different round than the result", () => {
    const validator = new TranscriptValidator();
    const first = freshRound("round-1");
    const second = freshRound("round-2");
    const report = validator.validate(second.result, first.a, second.b);
    assert.equal(report.ok, false);
    const codes = report.issues.map((issue) => issue.code);
    assert.ok(codes.includes("ROUND_MISMATCH"));
  });

  it("detects tampering with a recorded declaration", () => {
    const validator = new TranscriptValidator();
    const { a, b, result } = freshRound("round-1");
    const tampered: Declaration = { ...b, issuedAt: b.issuedAt + 1 };
    const report = validator.validate(result, a, tampered);
    assert.equal(report.ok, false);
    assert.ok(
      report.issues.some((issue) => issue.code === "DECLARATION_TAMPERED"),
    );
  });

  it("detects a forged negotiation outcome", () => {
    const validator = new TranscriptValidator();
    const { a, b, result } = freshRound("round-1");
    const forged = { ...result, config: ["video"] };
    const report = validator.validate(forged, a, b);
    assert.equal(report.ok, false);
    assert.ok(
      report.issues.some((issue) => issue.code === "TRANSCRIPT_MISMATCH"),
    );
  });

  it("flags stale declarations when an age limit is configured", () => {
    const validator = new TranscriptValidator({
      maxDeclarationAgeMs: 1000,
      now: () => FIXED_TIME + 60_000,
    });
    const { a, b, result } = freshRound("round-1");
    const report = validator.validate(result, a, b);
    assert.equal(report.ok, false);
    assert.ok(
      report.issues.some((issue) => issue.code === "STALE_DECLARATION"),
    );
  });
});
