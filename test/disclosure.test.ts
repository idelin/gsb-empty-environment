import test from "node:test";
import assert from "node:assert/strict";
import {
  FedCMValidationError,
  assertNoUndisclosedEmail,
  buildIdAssertionClaims,
  disclosureAllowsEmail,
  normalizeDisclosureTextShown,
  sanitizeAssertionClaims,
} from "../index.ts";

const account = {
  id: "alice",
  email: "alice@example.com",
  email_verified: true,
  name: "Alice",
  picture: "https://idp.example/alice.png",
};

test("only an affirmative disclosure value unlocks email", () => {
  for (const value of [true, "true", " TRUE "]) {
    assert.equal(normalizeDisclosureTextShown(value), true);
    assert.equal(disclosureAllowsEmail(value), true);
  }

  for (const value of [
    undefined,
    null,
    false,
    "false",
    "0",
    0,
    1,
    "",
    "yes",
  ]) {
    assert.equal(normalizeDisclosureTextShown(value), false, String(value));
    assert.equal(disclosureAllowsEmail(value), false, String(value));
  }
});

test("missing disclosure strips email claims from a built assertion", () => {
  const claims = buildIdAssertionClaims({
    account,
    disclosureTextShown: undefined,
  });

  assert.equal(claims.sub, "alice");
  assert.equal(claims.name, "Alice");
  assert.equal(claims.picture, "https://idp.example/alice.png");
  assert.equal("email" in claims, false);
  assert.equal("email_verified" in claims, false);
});

test("false disclosure strips email claims from a built assertion", () => {
  const claims = buildIdAssertionClaims({
    account,
    disclosureTextShown: false,
  });

  assert.equal("email" in claims, false);
  assert.equal("email_verified" in claims, false);
});

test("true disclosure includes the account email", () => {
  const claims = buildIdAssertionClaims({
    account,
    disclosureTextShown: true,
  });

  assert.equal(claims.email, "alice@example.com");
  assert.equal(claims.email_verified, true);
});

test("caller-supplied email claims are filtered when disclosure is absent", () => {
  const claims = buildIdAssertionClaims({
    account,
    disclosureTextShown: undefined,
    claims: {
      sub: "override-me",
      email: "leak@example.com",
      email_verified: true,
      email_address: "leak@example.com",
      role: "user",
    },
  });

  assert.equal(claims.sub, "alice");
  assert.equal(claims.role, "user");
  assert.equal("email" in claims, false);
  assert.equal("email_verified" in claims, false);
  assert.equal("email_address" in claims, false);
});

test("sanitizeAssertionClaims preserves non-email claims", () => {
  const claims = sanitizeAssertionClaims(
    { sub: "alice", email: "alice@example.com", name: "Alice" },
    "false",
  );

  assert.deepEqual(claims, { sub: "alice", name: "Alice" });
});

test("assertNoUndisclosedEmail throws only when email would leak", () => {
  assert.throws(
    () =>
      assertNoUndisclosedEmail(
        { sub: "alice", email: "alice@example.com" },
        undefined,
      ),
    (error) =>
      error instanceof FedCMValidationError &&
      error.code === "disclosure_not_shown",
  );

  assert.doesNotThrow(() =>
    assertNoUndisclosedEmail({ sub: "alice", name: "Alice" }, false),
  );

  assert.doesNotThrow(() =>
    assertNoUndisclosedEmail(
      { sub: "alice", email: "alice@example.com" },
      "true",
    ),
  );
});
