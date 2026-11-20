import assert from "node:assert/strict";
import test from "node:test";

import {
  computeDeclarationDigest,
  createRoutingAuthorization,
  mxRuleMatchesHost,
  selectMatchingRule,
  type DeclarationInput,
  type VerificationContext,
} from "../src/index.ts";

const DOMAIN = "example.com";
const MX_A = "mx-a.example.com";
const MX_B = "mx-b.example.com";
const KEY_A = "spki-sha256:aaaa";
const KEY_B = "spki-sha256:bbbb";

function declaration(
  overrides: Partial<DeclarationInput> &
    Pick<DeclarationInput, "sequence" | "previousDigest">,
): DeclarationInput {
  return {
    domain: DOMAIN,
    mxRules: [{ host: MX_A, publicKeyDigests: [KEY_A] }],
    notBefore: 1_000,
    notAfter: 2_000,
    revoked: false,
    ...overrides,
  };
}

function acceptingVerifier(
  observations: VerificationContext[] = [],
) {
  return async (
    _declaration: unknown,
    _digest: string,
    context: VerificationContext,
  ) => {
    observations.push(context);
    return true;
  };
}

test("expired declarations cannot start or finish a delivery", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const submitted = await authority.submit(
    declaration({ sequence: 0, previousDigest: null }),
  );
  assert.equal(submitted.accepted, true);

  const handshake = authority.beginHandshake(DOMAIN, MX_A, KEY_A);
  assert.ok(handshake);
  assert.equal(authority.finalizeDelivery(handshake), true);

  now = 2_001;
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);
  const final = authority.authorizeDelivery(handshake);
  assert.equal(final.authorized, false);
  assert.equal(final.reason, "expired");
});

test("a future declaration is retained but is unavailable until its start time", async () => {
  let now = 500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  await authority.submit(declaration({ sequence: 0, previousDigest: null }));
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);

  now = 1_000;
  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_A));
});

test("a successor arriving before its parent is queued and later activated", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const genesisDigest = computeDeclarationDigest(genesis);
  const successor = declaration({
    sequence: 1,
    previousDigest: genesisDigest,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });

  const early = await authority.submit(successor);
  assert.equal(early.accepted, true);
  assert.equal(early.queued, true);
  assert.equal(authority.getDomainState(DOMAIN).currentDigest, null);

  const parent = await authority.submit(genesis);
  assert.equal(parent.accepted, true);
  assert.equal(
    authority.getDomainState(DOMAIN).currentDigest,
    computeDeclarationDigest(successor),
  );
  assert.ok(authority.beginHandshake(DOMAIN, MX_B, KEY_B));
});

test("same sequence with different digests forks and immediately disables credentials", async () => {
  let now = 1_500;
  const observations: VerificationContext[] = [];
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(observations),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const branchA = declaration({ sequence: 1, previousDigest: digest0 });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);
  const inFlight = authority.beginHandshake(DOMAIN, MX_A, KEY_A);
  assert.ok(inFlight);

  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  await authority.submit(branchB);

  const state = authority.getDomainState(DOMAIN);
  assert.equal(state.fork, true);
  assert.equal(state.currentDigest, null);
  assert.deepEqual([...state.forkHeads].sort(), [digestA, digestB].sort());
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);
  assert.equal(authority.finalizeDelivery(inFlight), false);
  assert.equal(
    observations.find((entry) => entry.kind === "route")?.authority,
    "domain",
  );
});

test("trusted-root recovery resolves the fork, and a later old-branch event cannot undo it", async () => {
  let now = 1_500;
  const observations: VerificationContext[] = [];
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(observations),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const branchA = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_A, publicKeyDigests: [KEY_A] }],
  });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);

  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  await authority.submit(branchB);

  const recovery = declaration({
    kind: "recovery",
    sequence: 2,
    previousDigest: [digestA, digestB],
    mxRules: [{ host: "recovered.example.com", publicKeyDigests: ["key-r"] }],
  });
  const recoveryDigest = computeDeclarationDigest(recovery);
  const recovered = await authority.submit(recovery);
  assert.equal(recovered.accepted, true);
  assert.equal(recovered.recovered, true);
  assert.equal(
    observations.find((entry) => entry.kind === "recovery")?.authority,
    "trusted-root",
  );

  const handshake = authority.beginHandshake(
    DOMAIN,
    "recovered.example.com",
    "key-r",
  );
  assert.ok(handshake);
  assert.equal(authority.finalizeDelivery(handshake), true);

  const staleOldBranch = declaration({
    sequence: 2,
    previousDigest: digestA,
    mxRules: [{ host: MX_A, publicKeyDigests: [KEY_A] }],
  });
  const late = await authority.submit(staleOldBranch);
  assert.equal(late.accepted, true);

  const replayed = await authority.submit(branchB);
  assert.equal(replayed.duplicate, true);

  const recoveredState = authority.getDomainState(DOMAIN);
  assert.equal(recoveredState.currentDigest, recoveryDigest);
  assert.equal(recoveredState.forkSequence, null);
  assert.equal(recoveredState.fork, false);
  assert.equal(authority.finalizeDelivery(handshake), true);
  assert.ok(
    authority.beginHandshake(DOMAIN, "recovered.example.com", "key-r"),
  );
});

test("MX matching is label-based and wildcard covers exactly one label", () => {
  assert.equal(mxRuleMatchesHost(MX_A, MX_A), true);
  assert.equal(mxRuleMatchesHost(MX_A, "other.example.com"), false);
  assert.equal(mxRuleMatchesHost("*.example.com", "one.example.com"), true);
  assert.equal(mxRuleMatchesHost("*.example.com", "a.b.example.com"), false);
  assert.equal(mxRuleMatchesHost("*.example.com", "example.com"), false);
  assert.equal(mxRuleMatchesHost("mx-*.example.com", "mx-a.example.com"), false);
});

test("host and public key must be satisfied by one and the same rule", () => {
  const rules = [
    { host: MX_A, publicKeyDigests: [KEY_B] },
    { host: MX_B, publicKeyDigests: [KEY_A] },
  ];

  assert.equal(selectMatchingRule(rules, MX_A, KEY_A).matched, false);
  assert.equal(selectMatchingRule(rules, MX_B, KEY_B).matched, false);
  const match = selectMatchingRule(rules, MX_A, KEY_B);
  assert.equal(match.matched, true);
  assert.equal(match.ruleIndex, 0);
});

test("delivery handshakes never splice an MX from one rule with a key from another", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  await authority.submit(
    declaration({
      sequence: 0,
      previousDigest: null,
      mxRules: [
        { host: MX_A, publicKeyDigests: [KEY_B] },
        { host: MX_B, publicKeyDigests: [KEY_A] },
      ],
    }),
  );

  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);
  assert.equal(authority.beginHandshake(DOMAIN, MX_B, KEY_B), null);
  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_B));
  assert.ok(authority.beginHandshake(DOMAIN, MX_B, KEY_A));
});

test("a handshake is rejected when a revocation successor lands mid-handshake", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const handshake = authority.beginHandshake(DOMAIN, MX_A, KEY_A);
  assert.ok(handshake);

  const revocation = declaration({
    sequence: 1,
    previousDigest: digest0,
    revoked: true,
  });
  await authority.submit(revocation);

  const decision = authority.authorizeDelivery(handshake);
  assert.equal(decision.authorized, false);
  assert.equal(decision.reason, "stale-declaration");
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);

  const restored = declaration({
    sequence: 2,
    previousDigest: computeDeclarationDigest(revocation),
    revoked: false,
  });
  await authority.submit(restored);
  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_A));
});

test("tampered handshake fields are rejected and different domains advance independently", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const otherDomain = declaration({
    domain: "other.example",
    sequence: 1,
    previousDigest: "unseen-parent",
  });
  await authority.submit(otherDomain);
  assert.equal(authority.getDomainState("other.example").currentDigest, null);

  const genesis = declaration({ sequence: 0, previousDigest: null });
  await authority.submit(genesis);
  assert.equal(authority.getDomainState("other.example").currentDigest, null);
  assert.ok(authority.getDomainState(DOMAIN).currentDigest);

  const handshake = authority.beginHandshake(DOMAIN, MX_A, KEY_A);
  assert.ok(handshake);
  const tampered = { ...handshake, mx: MX_B };
  const decision = authority.authorizeDelivery(tampered);
  assert.equal(decision.authorized, false);
  assert.equal(decision.reason, "invalid-handshake");
});

test("recovery needs the trusted-root verifier rather than only the domain verifier", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verifyDomainDeclaration: () => true,
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);
  const branchA = declaration({ sequence: 1, previousDigest: digest0 });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);
  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  await authority.submit(branchB);

  const result = await authority.submit(
    declaration({
      kind: "recovery",
      sequence: 2,
      previousDigest: [digestA, digestB],
    }),
  );
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "no-verifier");
  assert.equal(authority.getDomainState(DOMAIN).fork, true);
});

test("a recovery can arrive before the second fork head and activate when the fork is complete", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const branchA = declaration({ sequence: 1, previousDigest: digest0 });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);

  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  const recovery = declaration({
    kind: "recovery",
    sequence: 2,
    previousDigest: [digestA, digestB],
  });

  const queued = await authority.submit(recovery);
  assert.equal(queued.queued, true);
  assert.equal(authority.getDomainState(DOMAIN).fork, false);

  await authority.submit(branchB);
  const state = authority.getDomainState(DOMAIN);
  assert.equal(state.fork, false);
  assert.equal(state.recovered, true);
  assert.equal(
    state.currentDigest,
    computeDeclarationDigest(recovery),
  );
});

test("recovery must reference current branch tips, not an older head", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const branchA = declaration({ sequence: 1, previousDigest: digest0 });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);
  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  await authority.submit(branchB);

  const updatedA = declaration({ sequence: 2, previousDigest: digestA });
  const updatedADigest = computeDeclarationDigest(updatedA);
  await authority.submit(updatedA);

  const staleRecovery = declaration({
    kind: "recovery",
    sequence: 3,
    previousDigest: [digestA, digestB],
  });
  const stale = await authority.submit(staleRecovery);
  assert.equal(stale.accepted, false);
  assert.equal(stale.reason, "stale-recovery");
  assert.equal(authority.getDomainState(DOMAIN).fork, true);

  const freshRecovery = declaration({
    kind: "recovery",
    sequence: 3,
    previousDigest: [updatedADigest, digestB],
  });
  const fresh = await authority.submit(freshRecovery);
  assert.equal(fresh.accepted, true);
  assert.equal(authority.getDomainState(DOMAIN).fork, false);
});

test("forked branches can independently advance to the same sequence before recovery", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const genesis = declaration({ sequence: 0, previousDigest: null });
  const digest0 = computeDeclarationDigest(genesis);
  await authority.submit(genesis);

  const branchA = declaration({ sequence: 1, previousDigest: digest0 });
  const digestA = computeDeclarationDigest(branchA);
  await authority.submit(branchA);
  const branchB = declaration({
    sequence: 1,
    previousDigest: digest0,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const digestB = computeDeclarationDigest(branchB);
  await authority.submit(branchB);

  const advancedA = declaration({ sequence: 2, previousDigest: digestA });
  const advancedADigest = computeDeclarationDigest(advancedA);
  await authority.submit(advancedA);
  const advancedB = declaration({
    sequence: 2,
    previousDigest: digestB,
    mxRules: [{ host: MX_B, publicKeyDigests: [KEY_B] }],
  });
  const advancedBDigest = computeDeclarationDigest(advancedB);
  await authority.submit(advancedB);

  assert.equal(authority.getDomainState(DOMAIN).fork, true);
  assert.deepEqual(
    [...authority.getDomainState(DOMAIN).forkHeads].sort(),
    [advancedADigest, advancedBDigest].sort(),
  );

  const recovered = await authority.submit(
    declaration({
      kind: "recovery",
      sequence: 3,
      previousDigest: [advancedADigest, advancedBDigest],
      mxRules: [{ host: "recovered.example.com", publicKeyDigests: ["key-r"] }],
    }),
  );
  assert.equal(recovered.accepted, true);
  assert.equal(authority.getDomainState(DOMAIN).fork, false);
  assert.ok(
    authority.beginHandshake(DOMAIN, "recovered.example.com", "key-r"),
  );
});

test("a revoked declaration may carry no MX rules and blocks delivery until a successor", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  const revoked = declaration({
    sequence: 0,
    previousDigest: null,
    revoked: true,
    mxRules: [],
  });
  const revokedDigest = computeDeclarationDigest(revoked);
  await authority.submit(revoked);

  assert.equal(authority.getDomainState(DOMAIN).revoked, true);
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, KEY_A), null);

  await authority.submit(
    declaration({
      sequence: 1,
      previousDigest: revokedDigest,
      revoked: false,
    }),
  );
  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_A));
});

test("a rule with several key digests authorizes any one of them", async () => {
  let now = 1_500;
  const authority = createRoutingAuthorization({
    clock: () => now,
    verify: acceptingVerifier(),
  });

  await authority.submit(
    declaration({
      sequence: 0,
      previousDigest: null,
      mxRules: [{ host: MX_A, publicKeyDigests: [KEY_A, KEY_B] }],
    }),
  );

  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_A));
  assert.ok(authority.beginHandshake(DOMAIN, MX_A, KEY_B));
  assert.equal(authority.beginHandshake(DOMAIN, MX_A, "not-bound"), null);
});
