export type NodeId = string;

export interface NodeAddress {
  host: string;
  port: number;
}

export type MemberState = 'alive' | 'suspect' | 'dead';

export interface MemberMeta {
  id: NodeId;
  address: NodeAddress;
  heartbeat: number;
  state: MemberState;
  lastUpdate: number;
}

export interface DigestEntry {
  id: NodeId;
  heartbeat: number;
}

export type Message =
  | { type: 'BOOTSTRAP_REQUEST'; from: MemberMeta }
  | { type: 'BOOTSTRAP_RESPONSE'; members: MemberMeta[] }
  | { type: 'GOSSIP_SYNC'; from: NodeId; members: MemberMeta[] };

export interface BootstrapReport {
  contactedSeeds: number;
  totalSeeds: number;
  learnedMembers: number;
  knownMembers: number;
  durationMs: number;
}
