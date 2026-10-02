import {
  canonicalize,
  generateIdentity,
  sha256,
  signPayload,
  verifyPayload,
  type Identity,
} from './crypto.ts';
import { Region } from './region.ts';
import type { Decision, Tally, TopologyMode, Vote } from './types.ts';

export interface ProposalResult {
  decision: Decision;
  verified: boolean;
}

export class HierarchicalQuorumArbiter {
  private readonly regions = new Map<string, Region>();
  private primaryId: string | null = null;
  private readonly identity: Identity = generateIdentity();
  private epoch = 1;
  private mode: TopologyMode = 'hierarchical';
  private readonly commitLog: Decision[] = [];

  registerRegion(region: Region, options: { primary?: boolean } = {}): void {
    if (this.regions.has(region.id)) {
      throw new Error(`region ${region.id} already registered`);
    }
    this.regions.set(region.id, region);
    if (options.primary) {
      if (this.primaryId !== null) throw new Error('primary region already assigned');
      this.primaryId = region.id;
    }
  }

  get currentEpoch(): number {
    return this.epoch;
  }

  get currentMode(): TopologyMode {
    return this.mode;
  }

  get committedLog(): readonly Decision[] {
    return this.commitLog;
  }

  setReachable(regionId: string, reachable: boolean): void {
    const region = this.mustGetRegion(regionId);
    region.reachable = reachable;
    if (regionId === this.primaryId && !reachable && this.mode === 'hierarchical') {
      this.degrade();
    }
  }

  private degrade(): void {
    this.mode = 'degraded';
    this.epoch += 1;
  }

  rejoinPrimary(presentedEpoch: number): Decision[] {
    if (this.primaryId === null) throw new Error('no primary region configured');
    const primary = this.mustGetRegion(this.primaryId);
    if (!primary.reachable) {
      throw new Error(`primary ${this.primaryId} is still unreachable`);
    }
    if (presentedEpoch <= this.epoch) {
      throw new Error(
        `stale epoch ${presentedEpoch}: arbiter is already at epoch ${this.epoch}; ` +
          'refusing to roll back committed state',
      );
    }
    this.epoch = presentedEpoch;
    this.mode = 'hierarchical';
    return [...this.commitLog];
  }

  quorumRequired(mode: TopologyMode = this.mode): number {
    const basis = this.quorumWeightBasis(mode);
    return Math.floor(basis / 2) + 1;
  }

  quorumWeightBasis(mode: TopologyMode = this.mode): number {
    let basis = 0;
    for (const region of this.regions.values()) {
      if (mode === 'degraded') {
        if (region.id !== this.primaryId) basis += region.weight;
      } else {
        basis += region.weight;
      }
    }
    return basis;
  }

  propose(
    proposalId: string,
    payload: string,
    localApprovals: Record<string, number> = {},
  ): ProposalResult {
    if (this.mode === 'hierarchical' && this.primaryId !== null) {
      const primary = this.mustGetRegion(this.primaryId);
      if (!primary.reachable) this.degrade();
    }

    const votes: Vote[] = [];
    for (const region of this.regions.values()) {
      if (!region.reachable) continue;
      if (this.mode === 'degraded' && region.id === this.primaryId) continue;
      const approvals = localApprovals[region.id] ?? region.replicaCount;
      votes.push(region.castVote(proposalId, this.epoch, this.mode, approvals));
    }

    const tally = this.tallyVotes(votes);
    const outcome = this.quorumSatisfied(tally, votes) ? 'committed' : 'rejected';

    const unsignedDecision: Omit<Decision, 'decisionSignature'> = {
      proposalId,
      payloadHash: sha256(payload),
      epoch: this.epoch,
      mode: this.mode,
      outcome,
      tally,
      evidence: votes,
      arbiterPublicKey: this.identity.publicKeyPem,
    };
    const decision: Decision = {
      ...unsignedDecision,
      decisionSignature: signPayload(this.identity, canonicalize(unsignedDecision)),
    };
    if (outcome === 'committed') this.commitLog.push(decision);
    return { decision, verified: this.verifyDecision(decision) };
  }

  private tallyVotes(votes: Vote[]): Tally {
    let approveWeight = 0;
    let rejectWeight = 0;
    for (const vote of votes) {
      if (vote.approve) approveWeight += vote.weight;
      else rejectWeight += vote.weight;
    }
    return {
      approveWeight,
      rejectWeight,
      quorumRequired: this.quorumRequired(),
      quorumWeightBasis: this.quorumWeightBasis(),
    };
  }

  private quorumSatisfied(tally: Tally, votes: Vote[]): boolean {
    if (tally.approveWeight < tally.quorumRequired) return false;
    if (this.mode === 'hierarchical' && this.primaryId !== null) {
      const primaryVote = votes.find((vote) => vote.regionId === this.primaryId);
      if (!primaryVote || !primaryVote.approve) return false;
    }
    return true;
  }

  verifyDecision(decision: Decision): boolean {
    const { decisionSignature, ...unsignedDecision } = decision;
    if (
      !verifyPayload(
        decision.arbiterPublicKey,
        canonicalize(unsignedDecision),
        decisionSignature,
      )
    ) {
      return false;
    }
    for (const vote of decision.evidence) {
      const { signature, ...unsignedVote } = vote;
      if (!verifyPayload(vote.publicKey, canonicalize(unsignedVote), signature)) {
        return false;
      }
    }
    const approveWeight = decision.evidence
      .filter((vote) => vote.approve)
      .reduce((sum, vote) => sum + vote.weight, 0);
    if (approveWeight !== decision.tally.approveWeight) return false;
    if (decision.outcome === 'committed') {
      if (approveWeight < decision.tally.quorumRequired) return false;
      if (decision.mode === 'hierarchical' && this.primaryId !== null) {
        const primaryVote = decision.evidence.find(
          (vote) => vote.regionId === this.primaryId,
        );
        if (!primaryVote || !primaryVote.approve) return false;
      }
    }
    return true;
  }

  private mustGetRegion(regionId: string): Region {
    const region = this.regions.get(regionId);
    if (!region) throw new Error(`unknown region ${regionId}`);
    return region;
  }
}
