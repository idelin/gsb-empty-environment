export type NodeId = string;

export type NodeState = "follower" | "candidate" | "leader";

/** A lease: who holds it, in which term, and until when (cluster clock ms). */
export interface Lease {
  holderId: NodeId;
  term: number;
  expiresAt: number;
}

/** Request a lease grant (election or renewal). */
export interface LeaseRequest {
  type: "LeaseRequest";
  from: NodeId;
  term: number;
}

/**
 * Pre-vote probe sent before a real election. Granting a pre-vote has no
 * side effects (no lease granted, no timer reset); it only tells the
 * candidate whether a quorum *could* be reached, so isolated nodes do not
 * inflate terms or disturb a healthy majority.
 */
export interface PreVoteRequest {
  type: "PreVoteRequest";
  from: NodeId;
  term: number;
}

export interface PreVoteResponse {
  type: "PreVoteResponse";
  from: NodeId;
  term: number;
  granted: boolean;
  reason?: string;
}

/** Response to a lease request. `expiresAt` is set only when granted. */
export interface LeaseResponse {
  type: "LeaseResponse";
  from: NodeId;
  term: number;
  granted: boolean;
  expiresAt?: number;
  reason?: string;
}

export type Message = LeaseRequest | LeaseResponse | PreVoteRequest | PreVoteResponse;

/** Result of an accepted write. `term` doubles as a fencing token. */
export interface WriteAck {
  term: number;
  index: number;
  value: unknown;
}
