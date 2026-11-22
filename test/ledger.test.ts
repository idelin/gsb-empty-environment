import assert from "node:assert/strict";
import test from "node:test";
import {
  AuthorizationLedger,
  GENESIS_PARENT,
  MemoryPersistence,
  PausablePersistence,
  replayEvents,
  verifyCompressedSnapshot,
} from "../src/index.ts";
import type {
  AuthorizationInput,
  BlockInput,
  EventInput,
  RecoverSigner,
} from "../src/index.ts";

const recoverSigner: RecoverSigner = (authorization, signature) => {
  return signature === `sig:${authorization.issuer}` ? authorization.issuer : "0xwrong";
};

function auth(overrides: Partial<AuthorizationInput> = {}): AuthorizationInput {
  return {
    chainId: "chain-a",
    contract: "0xcontract",
    issuer: "0xalice",
    beneficiary: "0xbob",
    totalAmount: 1000n,
    validFrom: 0,
    validTo: 100,
    authorizationId: "auth-1",
    ...overrides,
  };
}

function spend(
  authorization: AuthorizationInput,
  amount: bigint,
  requestId: string,
  sequence = 0,
): EventInput {
  return {
    kind: "spend",
    authorization,
    signature: `sig:${authorization.issuer}`,
    amount,
    requestId,
    sequence,
  };
}

function block(
  hash: string,
  parentHash: string,
  number: number,
  events: EventInput[] = [],
): BlockInput {
  return {
    chainId: events[0]?.authorization?.chainId ?? "chain-a",
    hash,
    parentHash,
    number,
    events,
  };
}

test("partial spends accumulate and duplicate business requests are isolated by signed authorization", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 100n, "r1")]));
  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 250n, "r2")]));

  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 650n);
  assert.deepEqual(ledger.getUsedRequests("chain-a", "auth-1"), ["r1", "r2"]);
  assert.throws(
    () =>
      ledger.appendBlock(
        block("0xb3", "0xb2", 3, [spend(authorization, 10n, "r1")]),
      ),
    /already used/,
  );

  const otherSignature = auth({ authorizationId: "auth-2" });
  ledger.appendBlock(
    block("0xb3", "0xb2", 3, [
      {
        kind: "spend",
        authorization: otherSignature,
        signature: "sig:0xalice",
        amount: 10n,
        requestId: "r1",
        sequence: 0,
      },
    ]),
  );
  assert.equal(ledger.getRemaining("chain-a", "auth-2"), 990n);
});

test("branch switch replays the winning branch and isolates same signature request use", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "common")]));

  ledger.appendBlock(block("0xa2", "0xb1", 2, [spend(authorization, 200n, "branch-a")]));
  const branchAKey = ledger.getAuthorizationState("chain-a", "auth-1")!.key;
  assert.equal(ledger.getRemaining("chain-a", branchAKey), 500n);

  ledger.switchHead("0xb1");
  assert.equal(ledger.getRemaining("chain-a", branchAKey), 700n);

  // The same signature may use the same business request id on the new branch.
  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 250n, "branch-a")]));
  assert.equal(ledger.getRemaining("chain-a", branchAKey), 450n);
  assert.equal(ledger.getHead("chain-a")!.hash, "0xb2");

  ledger.switchHead("chain-a", "0xa2");
  assert.equal(ledger.getRemaining("chain-a", branchAKey), 500n);
  assert.deepEqual(ledger.getUsedRequests("chain-a", branchAKey), ["branch-a", "common"]);
});

test("revocations and invalidations disappear precisely when leaving a branch", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 100n, "r1")]));
  ledger.appendBlock(
    block("0xa2", "0xb1", 2, [
      {
        kind: "revoke",
        authorization,
        signature: "sig:0xalice",
        sequence: 0,
      },
      {
        kind: "invalidate",
        authorization,
        signature: "sig:0xalice",
        requestId: "r1",
        sequence: 1,
      },
    ]),
  );
  const key = ledger.getAuthorizationState("chain-a", "auth-1")!.key;
  assert.equal(ledger.getAvailable("chain-a", key), 0n);
  assert.equal(ledger.getRequestStatus("chain-a", key, "r1"), "invalidated");

  ledger.switchHead("0xb1");
  assert.equal(ledger.getAvailable("chain-a", key), 900n);
  assert.equal(ledger.getRequestStatus("chain-a", key, "r1"), "active");

  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 50n, "r2")]));
  assert.equal(ledger.getRemaining("chain-a", key), 850n);
});

test("finality fixes an ancestor and rejects incompatible heads and rewrites", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "r1")]));
  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 200n, "r2")]));
  const finalized = ledger.finalize("chain-a", "0xb1");
  assert.equal(finalized.blockHash, "0xb1");
  assert.deepEqual(ledger.getFinalizedState("chain-a")[ledger.getAuthorizationState("chain-a", "auth-1")!.key].consumed, 300n);

  // A known but incompatible branch can be stored, but cannot become head.
  ledger.appendBlock(block("0xb1x", GENESIS_PARENT, 1, [spend(authorization, 900n, "other")]));
  assert.throws(() => ledger.switchHead("0xb1x"), /incompatible with finalized/);
  assert.throws(() => ledger.finalize("0xb1x"), /current head|conflicts/);
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 500n);

  // Descendants of the finalized block remain legal.
  ledger.appendBlock(block("0xb3", "0xb2", 3, [spend(authorization, 100n, "r3")]));
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 400n);
  ledger.switchHead("0xb1");
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 700n);
  ledger.appendBlock(
    block("0xb1x2", "0xb1", 2, [spend(authorization, 10n, "late")]),
  );
  assert.equal(ledger.getHead("chain-a")!.hash, "0xb1x2");
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 690n);
  ledger.finalize("0xb1x2");
  assert.throws(() => ledger.switchHead("0xb2"), /incompatible with finalized/);
});

test("finality certificates must carry the stored ancestor path", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0fc1", GENESIS_PARENT, 1, [spend(authorization, 10n, "r1")]));
  ledger.appendBlock(block("0fc2", "0fc1", 2, [spend(authorization, 10n, "r2")]));

  assert.throws(
    () =>
      ledger.finalize({
        chainId: "chain-a",
        blockHash: "0fc2",
        ancestors: ["0xdead", "0fc2"],
      }),
    /ancestor path|prefix/,
  );
  const certificate = ledger.finalize({
    chainId: "chain-a",
    blockHash: "0fc2",
    ancestors: ["0fc1", "0fc2"],
  });
  assert.deepEqual(certificate.ancestors, ["0fc1", "0fc2"]);
});

test("validity interval is inclusive at both boundary blocks", () => {
  const authorization = auth({ validFrom: 10, validTo: 20, totalAmount: 100n });

  const before = new AuthorizationLedger({ recoverSigner });
  assert.throws(
    () => before.appendBlock(block("0xb9", GENESIS_PARENT, 9, [spend(authorization, 1n, "r")])),
    /outside authorization validity/,
  );

  const lower = new AuthorizationLedger({ recoverSigner });
  lower.appendBlock(block("0xb10", GENESIS_PARENT, 10, [spend(authorization, 40n, "r1")]));
  assert.equal(lower.getRemaining("chain-a", "auth-1"), 60n);

  // A root block may represent the upper boundary without fabricating eleven
  // unrelated intermediate blocks.
  const upper = new AuthorizationLedger({ recoverSigner });
  upper.appendBlock(block("0xb20", GENESIS_PARENT, 20, [spend(authorization, 60n, "r1")]));
  assert.equal(upper.getRemaining("chain-a", "auth-1"), 40n);

  const after = new AuthorizationLedger({ recoverSigner });
  assert.throws(
    () => after.appendBlock(block("0xb21", GENESIS_PARENT, 21, [spend(authorization, 1n, "r")])),
    /outside authorization validity/,
  );
});

test("allowances above Number.MAX_SAFE_INTEGER stay exact", () => {
  const huge = 9_007_199_254_740_993n;
  const authorization = auth({ totalAmount: huge });
  const ledger = new AuthorizationLedger({ recoverSigner });
  ledger.appendBlock(block("0bh1", GENESIS_PARENT, 1, [spend(authorization, 1n, "r1")]));
  ledger.appendBlock(block("0bh2", "0bh1", 2, [spend(authorization, huge - 2n, "r2")]));
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 1n);
  assert.throws(
    () => ledger.appendBlock(block("0bh3", "0bh2", 3, [spend(authorization, 2n, "r3")])),
    /would consume/,
  );

  assert.throws(
    () =>
      new AuthorizationLedger({ recoverSigner }).appendBlock(
        block("0bx", GENESIS_PARENT, 1, [
          spend(auth({ totalAmount: Number.MAX_SAFE_INTEGER + 1 }), 1n, "r"),
        ]),
      ),
    /safe integer/,
  );
});

test("compressed snapshot proofs bind total, consumed, request root and reject broken paths", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "r1")]));
  const key = ledger.getAuthorizationState("chain-a", "auth-1")!.key;

  const snapshot = ledger.createCompressedSnapshot([
    {
      state: {
        authorization,
        signature: "sig:0xalice",
        totalAmount: 1000n,
        consumed: 450n,
        requests: [{ requestId: "r1", status: "active" }],
      },
    },
  ]);
  assert.equal(snapshot.baseRoot, ledger.getCommitmentRoot("chain-a"));
  assert.notEqual(snapshot.root, snapshot.baseRoot);
  verifyCompressedSnapshot(snapshot, snapshot.baseRoot);

  const missingSibling = structuredClone(snapshot);
  (missingSibling.changes[0].proof.siblings as unknown as unknown[]).pop();
  assert.throws(
    () => verifyCompressedSnapshot(missingSibling, snapshot.baseRoot),
    /missing sibling/,
  );

  const sameHeightConflict = structuredClone(snapshot);
  (sameHeightConflict.changes[0].proof.siblings as unknown as any[])[1].level = 0;
  assert.throws(
    () => verifyCompressedSnapshot(sameHeightConflict, snapshot.baseRoot),
    /two roots at height|two snapshot proofs disagree|wrong side/,
  );

  const requestRootTamper = structuredClone(snapshot);
  (requestRootTamper.changes[0].state as { consumed: bigint }).consumed = 451n;
  assert.throws(
    () => verifyCompressedSnapshot(requestRootTamper, snapshot.baseRoot),
    /new leaf|root does not commit/,
  );

  const ledgerVerification = ledger.submitCompressedSnapshot(snapshot);
  assert.equal(ledgerVerification.root, snapshot.root);
  assert.equal(ledger.getRemaining("chain-a", key), 550n);

  const secondSnapshot = ledger.createCompressedSnapshot([
    {
      state: {
        authorization,
        signature: "sig:0xalice",
        consumed: 500n,
        requests: [{ requestId: "r1", status: "active" }],
      },
    },
  ]);
  assert.equal(secondSnapshot.baseRoot, snapshot.root);
  ledger.applyCompressedSnapshot(secondSnapshot);
  assert.equal(ledger.getRemaining("chain-a", key), 500n);
});

test("compressed snapshots update multiple changed paths without conflicting siblings", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const first = auth({ authorizationId: "a1" });
  const second = auth({ authorizationId: "a2" });
  ledger.appendBlock(
    block("0bm1", GENESIS_PARENT, 1, [
      spend(first, 100n, "r1", 0),
      spend(second, 200n, "r2", 1),
    ]),
  );

  const snapshot = ledger.createCompressedSnapshot([
    {
      state: {
        authorization: first,
        signature: "sig:0xalice",
        consumed: 150n,
        requests: [{ requestId: "r1", status: "active" }],
      },
    },
    {
      state: {
        authorization: second,
        signature: "sig:0xalice",
        consumed: 300n,
        requests: [{ requestId: "r2", status: "active" }],
      },
    },
  ]);
  assert.equal(snapshot.changes.length, 2);
  assert.equal(verifyCompressedSnapshot(snapshot, snapshot.baseRoot).root, snapshot.root);
});

test("an accepted snapshot overlay survives atomic persistence", async () => {
  const persistence = new MemoryPersistence();
  const ledger = new AuthorizationLedger({ recoverSigner, persistence });
  const authorization = auth();
  ledger.appendBlock(block("0bs1", GENESIS_PARENT, 1, [spend(authorization, 100n, "r1")]));
  const snapshot = ledger.createCompressedSnapshot([
    {
      state: {
        authorization,
        signature: "sig:0xalice",
        consumed: 700n,
        requests: [{ requestId: "r1", status: "active" }],
      },
    },
  ]);
  ledger.applyCompressedSnapshot(snapshot);
  await ledger.save();

  const restored = await AuthorizationLedger.load({ recoverSigner, persistence });
  assert.ok(restored);
  assert.equal(restored.getRemaining("chain-a", "auth-1"), 300n);
  assert.equal(restored.getCommitmentRoot("chain-a"), snapshot.root);
});

test("exported state is JSON-safe and restores history, finality and bigint amounts", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth({ totalAmount: 9_007_199_254_740_993n });
  ledger.appendBlock(block("0bj1", GENESIS_PARENT, 1, [spend(authorization, 1n, "r1")]));
  ledger.appendBlock(block("0bj2", "0bj1", 2, [spend(authorization, 2n, "r2")]));
  ledger.finalize("0bj2");

  const encoded = JSON.parse(JSON.stringify(ledger.exportState()));
  const restored = AuthorizationLedger.fromState(encoded, { recoverSigner });
  assert.equal(restored.getHead("chain-a")!.hash, "0bj2");
  assert.equal(restored.getFinality("chain-a")!.blockHash, "0bj2");
  assert.equal(restored.getHistory("chain-a").length, 2);
  assert.equal(restored.getRemaining("chain-a", "auth-1"), 9_007_199_254_740_990n);
});

test("persistence commit is an atomic bundle of history, head and finality", async () => {
  const persistence = new MemoryPersistence({ failAtCommit: 2 });
  const ledger = new AuthorizationLedger({ recoverSigner, persistence });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "r1")]));
  await ledger.save();

  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 200n, "r2")]));
  ledger.finalize("0xb2");
  await assert.rejects(() => ledger.save(), /simulated power loss/);

  const restored = await AuthorizationLedger.load({ recoverSigner, persistence });
  assert.ok(restored);
  assert.equal(restored.getHead("chain-a")!.hash, "0xb1");
  assert.equal(restored.getHistory("chain-a").length, 1);
  assert.equal(restored.getFinality("chain-a"), undefined);
  assert.equal(restored.getRemaining("chain-a", "auth-1"), 700n);
});

test("pauseable persistence never exposes an intermediate tuple", async () => {
  const inner = new MemoryPersistence();
  const persistence = new PausablePersistence(inner);
  const ledger = new AuthorizationLedger({ recoverSigner, persistence });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "r1")]));

  persistence.pause();
  await ledger.save();
  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 200n, "r2")]));
  ledger.finalize("0xb2");
  await ledger.save();
  assert.equal(inner.peek(), null);

  await persistence.resume();
  const restored = await AuthorizationLedger.load({ recoverSigner, persistence });
  assert.ok(restored);
  assert.equal(restored.getHead("chain-a")!.hash, "0xb2");
  assert.equal(restored.getHistory("chain-a").length, 2);
  assert.equal(restored.getFinality("chain-a")!.blockHash, "0xb2");
});

test("independent replay matches ledger state for a selected fork", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const authorization = auth();
  ledger.appendBlock(block("0xb1", GENESIS_PARENT, 1, [spend(authorization, 300n, "common")]));
  ledger.appendBlock(block("0xa2", "0xb1", 2, [spend(authorization, 200n, "a")]));
  ledger.appendBlock(block("0xb2", "0xb1", 2, [spend(authorization, 250n, "b")]));
  ledger.switchHead("0xb2");

  const history = ledger.getHistory("0xb2");
  const replay = replayEvents(history, recoverSigner, { headHash: "0xb2" });
  const key = ledger.getAuthorizationState("chain-a", "auth-1")!.key;
  assert.equal(replay.headHash, "0xb2");
  assert.equal(replay.remainingByKey[key], ledger.getRemaining("chain-a", key));
  assert.deepEqual(replay.usedRequestsByKey[key], ["b", "common"]);
});

test("different chains keep the same signed fields isolated", () => {
  const ledger = new AuthorizationLedger({ recoverSigner });
  const first = auth({ chainId: "chain-a" });
  const second = auth({ chainId: "chain-b" });
  ledger.appendBlock(block("0xa1", GENESIS_PARENT, 1, [spend(first, 100n, "same")]));
  ledger.appendBlock({
    ...block("0xb1", GENESIS_PARENT, 1, [spend(second, 200n, "same")]),
    chainId: "chain-b",
  });
  assert.equal(ledger.getRemaining("chain-a", "auth-1"), 900n);
  assert.equal(ledger.getRemaining("chain-b", "auth-1"), 800n);
});
