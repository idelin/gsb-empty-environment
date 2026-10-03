export type NodeId = string;

export type FaultType = 'node-crash' | 'packet-loss';

/**
 * A ground-truth fault injected into the cluster.
 * `cycle` is the discrete protocol cycle in which the fault begins;
 * `phase` is the position within that cycle in [0, 1) — a node probed
 * at a phase earlier than the crash phase still answers once in the
 * crash cycle, which models mid-cycle failures realistically.
 */
export interface FaultEvent {
  readonly eventId: string;
  readonly type: FaultType;
  readonly nodeId: NodeId;
  readonly cycle: number;
  readonly phase: number;
}

export type DetectorEventKind = 'suspected' | 'refuted' | 'marked-dead';

/** An observation emitted by the SWIM failure detector. */
export interface DetectorEvent {
  readonly cycle: number;
  readonly nodeId: NodeId;
  readonly kind: DetectorEventKind;
}
