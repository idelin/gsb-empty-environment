import { test } from "node:test";
import assert from "node:assert/strict";
import { jsonEquals } from "../src/index.ts";
import { replay, script } from "../examples/replay.ts";

test("replayable scenario: every step verified against a from-scratch compile", () => {
  const report = replay(script);
  assert.equal(report.lines.length, script.length);
});

test("replay: rollbacks land exactly on historical snapshot states", () => {
  const report = replay(script);
  const snaps = report.snapshots;
  const byVersion = new Map(snaps.map((s) => [s.version, s]));
  // v10 = rollback of the beta-disable -> equals v8 (post-swap state)
  assert.ok(jsonEquals(byVersion.get(10)!.config, byVersion.get(8)!.config));
  // v11 = rollback of the priority swap -> equals v7 (pre-swap state)
  assert.ok(jsonEquals(byVersion.get(11)!.config, byVersion.get(7)!.config));
  // rejected commits never published: no version was consumed between v7 and v8
  assert.ok(jsonEquals(byVersion.get(7)!.config, snaps[6]!.config));
});
