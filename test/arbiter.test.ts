import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { HierarchicalQuorumArbiter } from '../src/arbiter.ts';
import { Region } from '../src/region.ts';
import type { Decision } from '../src/types.ts';

function buildCluster(): HierarchicalQuorumArbiter {
  const arbiter = new HierarchicalQuorumArbiter();
  arbiter.registerRegion(
    new Region({ id: 'IDC-A', weight: 2, localThreshold: 0.5, replicaCount: 4 }),
    { primary: true },
  );
  arbiter.registerRegion(
    new Region({ id: 'IDC-B', weight: 1, localThreshold: 0.5, replicaCount: 3 }),
  );
  arbiter.registerRegion(
    new Region({ id: 'IDC-C', weight: 1, localThreshold: 0.5, replicaCount: 3 }),
  );
  return arbiter;
}

describe('hierarchical quorum arbiter', () => {
  it('commits in hierarchical mode when weighted majority incl. primary approves', () => {
    const arbiter = buildCluster();
    const { decision, verified } = arbiter.propose('p-1', 'set x=1');
    assert.equal(decision.outcome, 'committed');
    assert.equal(decision.mode, 'hierarchical');
    assert.equal(decision.tally.approveWeight, 4);
    assert.equal(decision.tally.quorumRequired, 3);
    assert.equal(verified, true);
    assert.equal(decision.evidence.length, 3);
  });

  it('rejects when the primary vetoes despite sufficient weight', () => {
    const arbiter = buildCluster();
    const { decision } = arbiter.propose('p-2', 'set x=2', { 'IDC-A': 1 });
    assert.equal(decision.outcome, 'rejected');
    assert.equal(arbiter.committedLog.length, 0);
  });

  it('rejects when weight is below quorum even with primary approval', () => {
    const arbiter = buildCluster();
    const { decision } = arbiter.propose('p-3', 'set x=3', {
      'IDC-B': 0,
      'IDC-C': 0,
    });
    assert.equal(decision.tally.approveWeight, 2);
    assert.equal(decision.outcome, 'rejected');
  });

  it('degrades to a temporary B+C majority when IDC-A is lost', () => {
    const arbiter = buildCluster();
    const epochBefore = arbiter.currentEpoch;
    arbiter.setReachable('IDC-A', false);
    assert.equal(arbiter.currentMode, 'degraded');
    assert.ok(arbiter.currentEpoch > epochBefore);

    const { decision, verified } = arbiter.propose('p-4', 'set x=4');
    assert.equal(decision.mode, 'degraded');
    assert.equal(decision.outcome, 'committed');
    assert.equal(decision.tally.approveWeight, 2);
    assert.equal(decision.tally.quorumRequired, 2);
    assert.deepEqual(
      decision.evidence.map((vote) => vote.regionId).sort(),
      ['IDC-B', 'IDC-C'],
    );
    assert.equal(verified, true);
  });

  it('cannot reach degraded quorum with IDC-B alone', () => {
    const arbiter = buildCluster();
    arbiter.setReachable('IDC-A', false);
    arbiter.setReachable('IDC-C', false);
    const { decision } = arbiter.propose('p-5', 'set x=5');
    assert.equal(decision.tally.approveWeight, 1);
    assert.equal(decision.outcome, 'rejected');
  });

  it('resumes hierarchical control when IDC-A rejoins with a higher epoch', () => {
    const arbiter = buildCluster();
    arbiter.setReachable('IDC-A', false);
    arbiter.propose('p-6', 'set x=6');
    const degradedEpoch = arbiter.currentEpoch;

    arbiter.setReachable('IDC-A', true);
    const syncedLog = arbiter.rejoinPrimary(degradedEpoch + 1);
    assert.equal(arbiter.currentMode, 'hierarchical');
    assert.equal(arbiter.currentEpoch, degradedEpoch + 1);
    assert.equal(syncedLog.length, 1);
    assert.equal(syncedLog[0].proposalId, 'p-6');

    const { decision } = arbiter.propose('p-7', 'set x=7');
    assert.equal(decision.mode, 'hierarchical');
    assert.equal(decision.epoch, degradedEpoch + 1);
    assert.equal(decision.outcome, 'committed');
    assert.equal(arbiter.committedLog.length, 2);
  });

  it('rejects a stale epoch on rejoin, preventing state conflict', () => {
    const arbiter = buildCluster();
    arbiter.setReachable('IDC-A', false);
    arbiter.propose('p-8', 'set x=8');
    arbiter.setReachable('IDC-A', true);
    assert.throws(() => arbiter.rejoinPrimary(arbiter.currentEpoch), /stale epoch/);
    assert.throws(() => arbiter.rejoinPrimary(1), /stale epoch/);
    assert.equal(arbiter.currentMode, 'degraded');
  });

  it('verifies signature evidence and detects tampering', () => {
    const arbiter = buildCluster();
    const { decision } = arbiter.propose('p-9', 'set x=9');
    assert.equal(arbiter.verifyDecision(decision), true);

    const tamperedVote: Decision = structuredClone(decision);
    tamperedVote.evidence[0].approve = false;
    assert.equal(arbiter.verifyDecision(tamperedVote), false);

    const tamperedOutcome: Decision = structuredClone(decision);
    tamperedOutcome.outcome = 'rejected';
    assert.equal(arbiter.verifyDecision(tamperedOutcome), false);
  });

  it('enforces per-region local majority thresholds', () => {
    const arbiter = buildCluster();
    const { decision } = arbiter.propose('p-10', 'set x=10', {
      'IDC-B': 1,
      'IDC-C': 3,
    });
    const voteB = decision.evidence.find((vote) => vote.regionId === 'IDC-B');
    const voteC = decision.evidence.find((vote) => vote.regionId === 'IDC-C');
    assert.equal(voteB?.approve, false);
    assert.equal(voteC?.approve, true);
    assert.equal(decision.tally.approveWeight, 3);
    assert.equal(decision.outcome, 'committed');
  });
});
