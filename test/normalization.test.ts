import test from "node:test";
import assert from "node:assert/strict";

import {
  AccountIdentifierError,
  AccountLockoutTracker,
  LockoutStore,
  accountIdentity,
  normalizeEmail,
  normalizeUsername,
} from "../index.ts";

test("username case, fullwidth, combining characters and trailing dots share one key", () => {
  const variants = [
    "Alice",
    "ALICE",
    "AlIcE",
    "ＡＬＩＣＥ",
    "ＡＬＩＣＥ．",
    "Alice.",
    "ALICE...",
    "Alice。",
  ];
  for (const variant of variants) {
    assert.equal(normalizeUsername(variant), "alice");
  }

  assert.equal(normalizeUsername("Jose\u0301"), normalizeUsername("José"));
  assert.equal(normalizeUsername("José"), "josé");
  assert.equal(normalizeUsername("Straße"), normalizeUsername("STRASSE"));
});

test("obviously confusable and invisible username characters are rejected", () => {
  const confusables = ["аlice", "Αlice", "раураl"];
  for (const value of confusables) {
    assert.throws(
      () => normalizeUsername(value),
      (error: unknown) =>
        error instanceof AccountIdentifierError &&
        (error.code === "CONFUSABLE_CHARACTER" ||
          error.code === "MIXED_SCRIPTS"),
    );
  }

  assert.throws(
    () => normalizeUsername("ali\u200Bce"),
    (error: unknown) =>
      error instanceof AccountIdentifierError &&
      error.code === "INVISIBLE_CHARACTER",
  );
  assert.throws(
    () => normalizeUsername("ali ce"),
    (error: unknown) =>
      error instanceof AccountIdentifierError &&
      error.code === "INVISIBLE_CHARACTER",
  );
});

test("email local part preserves case while the domain is folded", () => {
  assert.equal(normalizeEmail("John.Doe@Example.COM"), "John.Doe@example.com");
  assert.equal(normalizeEmail("john.doe@EXAMPLE.COM"), "john.doe@example.com");
  assert.notEqual(
    normalizeEmail("John.Doe@example.com"),
    normalizeEmail("john.doe@example.com"),
  );
  assert.equal(
    normalizeEmail("John.Doe@ＥＸＡＭＰＬＥ．ＣＯＭ"),
    "John.Doe@example.com",
  );
  assert.equal(
    normalizeEmail("John.Doe@example.com."),
    "John.Doe@example.com",
  );
});

test("email aliases collapse for lockout identity but keep the display value", () => {
  assert.equal(
    normalizeEmail("victim+news@Example.COM"),
    "victim@example.com",
  );
  assert.equal(
    normalizeEmail("victim+news@example.com."),
    "victim@example.com",
  );
  assert.equal(
    normalizeEmail("john.doe+tag@GMAIL.COM"),
    "johndoe@gmail.com",
  );
  assert.equal(normalizeEmail("JohnDoe@googlemail.com"), "JohnDoe@gmail.com");

  const identity = accountIdentity("Victim+News@Example.COM");
  assert.equal(identity.key, "Victim@example.com");
  assert.equal(identity.display, "Victim+News@Example.COM");
  assert.equal(identity.original, "Victim+News@Example.COM");
});

test("case, fullwidth, combining, trailing dot and alias variants hit one lockout bucket", () => {
  const tracker = new AccountLockoutTracker({
    threshold: 5,
    now: () => 1_000_000,
  });
  const variants = [
    ["Alice", "password"],
    ["ＡＬＩＣＥ．", "otp"],
    ["ALICE...", "recovery"],
    ["AlIcE", "sso"],
    ["Alice．", "password"],
  ] as const;

  for (const [identifier, method] of variants) {
    const result = tracker.recordFailure(identifier, method);
    assert.equal(result.key, "alice");
    assert.equal(result.kind, "username");
  }

  const state = tracker.getState("alice");
  assert.ok(state);
  assert.equal(state.failureCount, 5);
  assert.equal(state.attemptCount, 5);
  assert.equal(state.lastMethod, "password");

  for (const [identifier] of variants) {
    assert.equal(tracker.isLocked(identifier), true);
    assert.equal(tracker.isLocked(identifier, state.generation), true);
  }
});

test("combining-character username variants share one counter", () => {
  const tracker = new AccountLockoutTracker({
    threshold: 2,
    now: () => 1_000_000,
  });
  tracker.recordFailure("Jose\u0301", "password");
  tracker.recordFailure("José", "otp");
  assert.equal(tracker.getState("José")?.failureCount, 2);
  assert.equal(tracker.isLocked("Jose\u0301"), true);
});

test("alias email variants share one counter across auth methods", () => {
  const tracker = new LockoutStore({ threshold: 4, now: () => 1_000_000 });
  tracker.recordFailure("victim+one@Example.COM", "password");
  tracker.recordFailure("victim+two@example.com.", "otp");
  tracker.recordFailure("victim@EXAMPLE.COM", "recovery");
  tracker.recordFailure("victim+three@example.com", "sso");

  assert.equal(tracker.getState("victim@example.com")?.failureCount, 4);
  assert.equal(tracker.isLocked("victim+four@EXAMPLE.COM."), true);
});

test("a successful retry resets the shared failure count", () => {
  const tracker = new AccountLockoutTracker({
    threshold: 3,
    now: () => 1_000_000,
  });
  tracker.recordFailure("Alice", "password");
  tracker.recordFailure("ALICE", "otp");
  const success = tracker.recordSuccess("ＡＬＩＣＥ．", "sso");

  assert.equal(success.outcome, "success");
  assert.equal(success.failureCount, 0);
  assert.equal(success.successCount, 1);
  assert.equal(success.attemptCount, 3);
  assert.equal(tracker.isLocked("alice"), false);

  tracker.recordFailure("Alice", "password");
  assert.equal(tracker.getState("alice")?.consecutiveFailures, 1);
  assert.equal(tracker.isLocked("alice"), false);
});

test("administrator unlock advances the generation and stale attempts are ignored", () => {
  const tracker = new AccountLockoutTracker({
    threshold: 2,
    now: () => 1_000_000,
  });
  tracker.recordFailure("Alice", "password");
  tracker.recordFailure("ALICE", "otp");
  assert.equal(tracker.isLocked("alice"), true);

  const oldGeneration = tracker.getGeneration("alice");
  const unlock = tracker.unlock("ＡＬＩＣＥ．");
  assert.equal(unlock.previousGeneration, oldGeneration);
  assert.equal(unlock.generation, oldGeneration + 1);
  assert.equal(unlock.unlocked, true);
  assert.equal(tracker.isLocked("alice"), false);
  assert.equal(tracker.getState("alice")?.failureCount, 0);

  const stale = tracker.recordFailure({
    raw: "ALICE",
    generation: oldGeneration,
  });
  assert.equal(stale.stale, true);
  assert.equal(stale.ignored, true);
  assert.equal(tracker.getState("alice")?.failureCount, 0);
  assert.equal(
    tracker.isGenerationCurrent("alice", oldGeneration),
    false,
  );

  tracker.recordFailure("alice", "password");
  assert.equal(tracker.getState("alice")?.failureCount, 1);
});

test("audit entries are keyed by the normalized value and retain the original", () => {
  const tracker = new AccountLockoutTracker({
    threshold: 2,
    now: () => 1_000_000,
  });
  tracker.recordFailure("ＡＬＩＣＥ．", "password");
  tracker.recordFailure("Alice", "otp");
  tracker.unlock("ALICE");

  const entries = tracker.getAuditLog("alice");
  assert.equal(entries.length, 3);
  assert.equal(entries[0].key, "alice");
  assert.equal(entries[0].normalized, "alice");
  assert.equal(entries[0].display, "ＡＬＩＣＥ．");
  assert.equal(entries[0].original, "ＡＬＩＣＥ．");
  assert.equal(entries[0].method, "password");
  assert.equal(entries[1].method, "otp");
  assert.equal(entries[2].outcome, "unlock");
  assert.equal(entries[2].generation, 1);
});
