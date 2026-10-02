import { generateIdentity, signPayload, canonicalize, type Identity } from './crypto.ts';
import type { RegionConfig, TopologyMode, Vote } from './types.ts';

export class Region {
  readonly id: string;
  readonly weight: number;
  readonly localThreshold: number;
  readonly replicaCount: number;
  readonly identity: Identity;
  reachable = true;

  constructor(config: RegionConfig) {
    if (config.weight < 1) throw new Error(`region ${config.id}: weight must be >= 1`);
    if (config.localThreshold <= 0 || config.localThreshold >= 1) {
      throw new Error(`region ${config.id}: localThreshold must be in (0, 1)`);
    }
    if (config.replicaCount < 1) {
      throw new Error(`region ${config.id}: replicaCount must be >= 1`);
    }
    this.id = config.id;
    this.weight = config.weight;
    this.localThreshold = config.localThreshold;
    this.replicaCount = config.replicaCount;
    this.identity = generateIdentity();
  }

  get publicKeyPem(): string {
    return this.identity.publicKeyPem;
  }

  localMajorityReached(localApprovals: number): boolean {
    return localApprovals / this.replicaCount > this.localThreshold;
  }

  castVote(
    proposalId: string,
    epoch: number,
    mode: TopologyMode,
    localApprovals: number,
  ): Vote {
    if (!this.reachable) {
      throw new Error(`region ${this.id} is unreachable and cannot vote`);
    }
    if (localApprovals < 0 || localApprovals > this.replicaCount) {
      throw new Error(`region ${this.id}: invalid localApprovals ${localApprovals}`);
    }
    const approve = this.localMajorityReached(localApprovals);
    const unsigned: Omit<Vote, 'signature'> = {
      regionId: this.id,
      proposalId,
      epoch,
      mode,
      approve,
      weight: this.weight,
      localApprovals,
      localReplicas: this.replicaCount,
      publicKey: this.publicKeyPem,
    };
    return { ...unsigned, signature: signPayload(this.identity, canonicalize(unsigned)) };
  }
}
