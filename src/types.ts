export type NodeId = string;

export interface Generation {
  readonly node: NodeId;
  readonly incarnation: number;
  readonly nonce: number;
}

export function genKey(g: Generation): string {
  return `${g.node}#${g.incarnation}:${g.nonce}`;
}

export function sameGen(a: Generation | null, b: Generation | null): boolean {
  return (
    a !== null &&
    b !== null &&
    a.node === b.node &&
    a.incarnation === b.incarnation &&
    a.nonce === b.nonce
  );
}

export const PacketKind = {
  Syn: 'SYN',
  SynAck: 'SYN-ACK',
  Ack: 'ACK',
  Data: 'DATA',
  Reset: 'RESET',
} as const;

export type PacketKind = (typeof PacketKind)[keyof typeof PacketKind];

export interface Packet {
  readonly kind: PacketKind;
  readonly src: NodeId;
  readonly dst: NodeId;
  readonly srcGen: Generation | null;
  readonly dstGen: Generation | null;
  readonly seq: number;
  readonly ack: number;
  readonly payload: string;
  readonly echoSrcGen?: Generation | null;
  readonly echoDstGen?: Generation | null;
  readonly echoSeq?: number;
}

export const ConnState = {
  Closed: 'CLOSED',
  SynSent: 'SYN-SENT',
  SynReceived: 'SYN-RECEIVED',
  Established: 'ESTABLISHED',
} as const;

export type ConnState = (typeof ConnState)[keyof typeof ConnState];
