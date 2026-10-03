export type EventKind = 'REQ_SEND' | 'REQ_RECV' | 'RESP_SEND' | 'RESP_RECV';

/** A single raw trace event stamped by a server's (possibly drifted) physical clock. */
export interface TraceEvent {
  readonly id: string;
  readonly server: string;
  readonly rpcId: string;
  readonly kind: EventKind;
  /** Physical local-clock reading, epoch milliseconds. */
  readonly time: number;
}

export interface CorrectedEvent extends TraceEvent {
  /** Physical time after applying the server's estimated clock offset. */
  readonly correctedTime: number;
}

export type EdgeKind = 'message' | 'process';

export interface CausalEdge {
  readonly from: string;
  readonly to: string;
  readonly kind: EdgeKind;
}

/** A message edge whose physical latency is negative before correction. */
export interface LatencyViolation {
  readonly rpcId: string;
  readonly fromServer: string;
  readonly toServer: string;
  readonly sendTime: number;
  readonly recvTime: number;
  readonly latencyMs: number;
}

export interface ClockCorrection {
  readonly server: string;
  /** Offset added to every raw timestamp of this server, in ms. */
  readonly offsetMs: number;
}

export interface Topology {
  readonly events: readonly CorrectedEvent[];
  readonly edges: readonly CausalEdge[];
  readonly corrections: readonly ClockCorrection[];
  /** Kahn topological order of event ids over the causal partial order. */
  readonly order: readonly string[];
  readonly violations: readonly LatencyViolation[];
}
