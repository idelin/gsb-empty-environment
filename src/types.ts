export type TopologyMode = 'hierarchical' | 'degraded';

export type DecisionOutcome = 'committed' | 'rejected';

export interface Vote {
  regionId: string;
  proposalId: string;
  epoch: number;
  mode: TopologyMode;
  approve: boolean;
  weight: number;
  localApprovals: number;
  localReplicas: number;
  publicKey: string;
  signature: string;
}

export interface Tally {
  approveWeight: number;
  rejectWeight: number;
  quorumRequired: number;
  quorumWeightBasis: number;
}

export interface Decision {
  proposalId: string;
  payloadHash: string;
  epoch: number;
  mode: TopologyMode;
  outcome: DecisionOutcome;
  tally: Tally;
  evidence: Vote[];
  arbiterPublicKey: string;
  decisionSignature: string;
}

export interface RegionConfig {
  id: string;
  weight: number;
  localThreshold: number;
  replicaCount: number;
}
