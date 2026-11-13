import assert from "node:assert/strict";
import test from "node:test";

import {
  CheckpointStore,
  Governance,
  GovernanceError,
  DelegationLedger,
  computeActionDigest,
  computeVotingPowerFromRecord,
  currentBalanceReviewCallback,
  findFlashLoanWitness,
  quorumRequired,
  reviewFlashLoanScenario,
  sha256Hex,
  snapshotReviewCallback,
} from "../index.ts";

test("checkpoints are tx ordered, strictly prior, and overwrite one position", () => {
  const store = new CheckpointStore();
  store.recordBalance("alice", [10, 0], 100);
  store.recordBalance("alice", [10, 2], 70);
  store.recordBalance("alice", [10, 2], 60);
  store.recordTotalSupply([10, 0], 500);
  store.recordTotalSupply([10, 2], 450);

  assert.equal(store.balanceAt("alice", [10, 0]), 0n);
  assert.equal(store.balanceAt("alice", [10, 1]), 100n);
  assert.equal(store.balanceAt("alice", [10, 2]), 100n);
  assert.equal(store.balanceAt("alice", [10, 3]), 60n);
  assert.equal(store.totalSupplyAt([10, 2]), 500n);
  assert.equal(store.totalSupplyAt([10, 3]), 450n);

  store.mint("alice", 40, [11, 0]);
  store.burn("alice", 10, [12, 0]);
  assert.equal(store.balanceAt("alice", [11, 1]), 100n);
  assert.equal(store.totalSupplyAt([11, 1]), 490n);
  assert.equal(store.balanceAt("alice", [12, 1]), 90n);
  assert.equal(store.totalSupplyAt([12, 1]), 480n);
});

test("delegation snapshots are forests and each underlying balance counts once", () => {
  const ledger = new DelegationLedger();
  ledger.setDelegate("a", "b", [1, 0]);
  ledger.setDelegate("b", "c", [1, 1]);
  assert.throws(
    () => ledger.setDelegate("c", "a", [1, 2]),
    (error: unknown) =>
      error instanceof GovernanceError && error.code === "DELEGATION_CYCLE"
  );

  assert.throws(
    () => ledger.setDelegate("b", "a", [2, 0]),
    (error: unknown) =>
      error instanceof GovernanceError && error.code === "DELEGATION_CYCLE"
  );

  assert.equal(
    computeVotingPowerFromRecord(
      "c",
      { a: 1, b: 2, c: 3, d: 4 },
      { a: "b", b: "c" }
    ),
    6n
  );
  assert.equal(
    computeVotingPowerFromRecord(
      "a",
      { a: 1, b: 2, c: 3, d: 4 },
      { a: "b", b: "c" }
    ),
    0n
  );
});

test("quorum uses exact rational ceiling", () => {
  assert.equal(quorumRequired(5, 1, 2), 3n);
  assert.equal(quorumRequired(1, 1, 3), 1n);
  assert.equal(quorumRequired(100, 2, 3), 67n);
  assert.equal(quorumRequired(0, 1, 2), 0n);
});

test("action digests are canonical and stable", () => {
  assert.equal(
    sha256Hex("abc"),
    "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
  );
  assert.equal(
    computeActionDigest({ b: 2n, a: [3, 1] }),
    computeActionDigest({ a: [3, 1], b: 2n })
  );
  assert.notEqual(
    computeActionDigest({ a: 1 }),
    computeActionDigest({ a: 2 })
  );
});

test("proposal freezes balances and delegation before P and binds execution", () => {
  const checkpoints = new CheckpointStore();
  checkpoints.writeSnapshot({ alice: 100, bob: 50, carol: 50 }, 200, [1, 0]);
  const delegations = new DelegationLedger();
  delegations.setDelegate("alice", "bob", [2, 0]);
  delegations.setDelegate("carol", "bob", [2, 1]);

  const governance = new Governance({
    checkpoints,
    delegations,
    config: {
      generation: 7,
      quorumNumerator: 1,
      quorumDenominator: 2,
      votingDelayBlocks: 1,
      votingPeriodBlocks: 2,
      queueDelayBlocks: 0,
      queueWindowBlocks: 10,
      executionDelayBlocks: 1,
      executionWindowBlocks: 5,
    },
  });

  const action = { target: "0xabc", value: 1, data: "0x1234" };
  const proposal = governance.createProposal({
    id: "p1",
    proposer: "alice",
    position: [10, 0],
    action,
  });

  assert.equal(proposal.snapshot.position.blockNumber, 10);
  assert.equal(proposal.snapshot.totalSupply, 200n);
  assert.equal(proposal.quorumRequired, 100n);
  assert.equal(governance.getVotingPower("p1", "bob"), 200n);
  assert.equal(governance.getVotingPower("p1", "alice"), 0n);

  // Changes after P, including changes in the same created block, cannot alter
  // the frozen snapshot.
  checkpoints.mint("bob", 10_000, [10, 1]);
  delegations.setDelegate("alice", "carol", [10, 1]);
  assert.equal(governance.getVotingPower("p1", "bob"), 200n);

  assert.equal(governance.getProposalStatus("p1", 10), "pending");
  assert.equal(governance.getProposalStatus("p1", 11), "active");

  const vote = governance.castVote("p1", "bob", "for", [11, 0]);
  assert.equal(vote.weight, 200n);

  // A later balance change in the voting transaction's block has no effect.
  checkpoints.mint("bob", 1_000, [11, 1]);
  assert.equal(governance.getProposal("p1")?.forVotes, 200n);

  assert.throws(
    () => governance.castVote("p1", "bob", "for", [11, 1]),
    (error: unknown) =>
      error instanceof GovernanceError && error.code === "VOTE_ALREADY_CAST"
  );
  assert.throws(
    () => governance.castVote("p1", "alice", "for", [11, 1]),
    (error: unknown) =>
      error instanceof GovernanceError && error.code === "NO_VOTING_POWER"
  );

  assert.equal(governance.getProposalStatus("p1", 13), "succeeded");
  assert.throws(
    () => governance.queueProposal("p1", [11, 2]),
    (error: unknown) =>
      error instanceof GovernanceError && error.code === "QUEUE_NOT_OPEN"
  );
  governance.queueProposal("p1", [13, 0]);
  assert.equal(governance.getProposalStatus("p1", 13), "queued");

  assert.throws(
    () =>
      governance.executeProposal(
        "p1",
        [14, 0],
        computeActionDigest({ target: "wrong" }),
        7
      ),
    (error: unknown) =>
      error instanceof GovernanceError &&
      error.code === "ACTION_DIGEST_MISMATCH"
  );
  assert.throws(
    () => governance.executeProposal("p1", [14, 0], proposal.actionDigest, 6),
    (error: unknown) =>
      error instanceof GovernanceError &&
      error.code === "CONFIG_GENERATION_MISMATCH"
  );

  governance.executeProposal("p1", [14, 0], proposal.actionDigest, 7);
  assert.equal(governance.getProposalStatus("p1", 14), "executed");
});

test("proposal snapshots respect txIndex order inside one block", () => {
  const checkpoints = new CheckpointStore();
  checkpoints.writeSnapshot({ alice: 10 }, 10, [20, 0]);
  checkpoints.writeSnapshot({ alice: 20 }, 20, [20, 2]);
  const governance = new Governance({
    checkpoints,
    config: {
      generation: 1,
      quorumNumerator: 1,
      quorumDenominator: 1,
      votingDelayBlocks: 0,
      votingPeriodBlocks: 1,
    },
  });

  const beforeSecond = governance.createProposal({
    id: "tx-before",
    proposer: "alice",
    blockNumber: 20,
    txIndex: 1,
    action: { n: 1 },
  });
  const atSecond = governance.createProposal({
    id: "tx-at",
    proposer: "alice",
    position: [20, 2],
    action: { n: 2 },
  });
  const afterSecond = governance.createProposal({
    id: "tx-after",
    proposer: "alice",
    position: [20, 3],
    action: { n: 3 },
  });

  assert.equal(beforeSecond.snapshot.balanceOf("alice"), 10n);
  assert.equal(atSecond.snapshot.balanceOf("alice"), 10n);
  assert.equal(afterSecond.snapshot.balanceOf("alice"), 20n);
  assert.equal(beforeSecond.snapshot.totalSupply, 10n);
  assert.equal(atSecond.snapshot.totalSupply, 10n);
  assert.equal(afterSecond.snapshot.totalSupply, 20n);
});

test("config generation change invalidates execution", () => {
  const governance = new Governance({
    config: {
      generation: 1,
      quorumNumerator: 1,
      quorumDenominator: 2,
      votingDelayBlocks: 0,
      votingPeriodBlocks: 1,
      queueDelayBlocks: 0,
      executionDelayBlocks: 0,
      executionWindowBlocks: 2,
    },
  });
  governance.checkpoints.writeSnapshot({ a: 100, b: 100 }, 200, [1, 0]);
  const proposal = governance.createProposal({
    id: "p2",
    proposer: "a",
    position: [2, 0],
    action: { x: 1 },
  });
  governance.checkpoints.writeSnapshot({ a: 100, b: 100 }, 200, [2, 1]);
  governance.castVote({
    proposalId: "p2",
    account: "a",
    support: "for",
    blockNumber: 2,
    txIndex: 1,
  });
  governance.queueProposal({
    proposalId: "p2",
    blockNumber: 3,
    txIndex: 0,
  });
  governance.updateConfig({
    generation: 2,
    quorumNumerator: 1,
    quorumDenominator: 2,
    votingDelayBlocks: 0,
    votingPeriodBlocks: 1,
    queueDelayBlocks: 0,
    executionDelayBlocks: 0,
    executionWindowBlocks: 2,
  });

  assert.throws(
    () =>
      governance.executeProposal({
        proposalId: "p2",
        blockNumber: 3,
        txIndex: 1,
        actionDigest: proposal.actionDigest,
        configGeneration: 1,
      }),
    (error: unknown) =>
      error instanceof GovernanceError &&
      error.code === "CONFIG_GENERATION_MISMATCH"
  );
});

test("flash-loan review finds a current-balance witness and snapshot is safe", () => {
  const review = reviewFlashLoanScenario();
  assert.ok(review.currentBalanceWitness);
  assert.equal(review.snapshotWitness, null);
  assert.equal(review.snapshotModelSafe, true);

  const witness = review.currentBalanceWitness;
  assert.equal(witness.changedResult, true);
  assert.equal(witness.baselineOutcome, "defeated");
  assert.equal(witness.attackedOutcome, "passed");
  assert.equal(witness.balanceBefore, witness.balanceAfter);
  assert.equal(witness.netBalanceRestored, true);
  assert.ok(witness.transactionCount <= 2);
  assert.equal(witness.blockNumber, 100);

  const operations = witness.transactions.flatMap(
    (transaction) => transaction.operations
  );
  assert.deepEqual(
    operations.map((operation) => operation.type),
    ["borrow", "vote", "repay"]
  );

  assert.deepEqual(currentBalanceReviewCallback(), review.currentBalanceWitness);
  assert.equal(snapshotReviewCallback(), null);
  assert.equal(findFlashLoanWitness(undefined, "snapshot"), null);
});

test("flash-loan search respects one or two same-block transaction slots", () => {
  const one = findFlashLoanWitness({
    accounts: { attacker: 1, lender: 9, voter: 5 },
    attacker: "attacker",
    againstVoters: ["voter"],
    quorumNumerator: 1,
    quorumDenominator: 2,
    maxTransactions: 1,
  });
  assert.ok(one);
  assert.equal(one.transactionCount, 1);
  assert.equal(one.transactions[0].txIndex, 0);

  const two = findFlashLoanWitness({
    accounts: { attacker: 1, lender: 9, voter: 5 },
    attacker: "attacker",
    againstVoters: ["voter"],
    quorumNumerator: 1,
    quorumDenominator: 2,
    minTransactions: 2,
    maxTransactions: 2,
  });
  assert.ok(two);
  assert.equal(two.transactionCount, 2);
  assert.deepEqual(
    two.transactions.map((transaction) => transaction.txIndex),
    [0, 1]
  );
});
