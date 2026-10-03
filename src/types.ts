export const ALIVE = 0;
export const DEAD = 1;

export interface MemberRecord {
  incarnation: number;
  state: number; // ALIVE | DEAD
  version: number; // lamport-style; in this sim stamped with virtual ms
}

export interface DigestEntry extends MemberRecord {
  id: number;
}

export type Message =
  | { kind: 'hb'; from: number; entries: DigestEntry[] }
  | { kind: 'event'; from: number; entries: DigestEntry[] }
  | { kind: 'digest'; from: number; cellId: number; entries: DigestEntry[] }
  | { kind: 'digestAck'; from: number; cellId: number; entries: DigestEntry[] }
  | { kind: 'linkReset'; deadId: number }
  | { kind: 'flat'; from: number; entries: DigestEntry[] };
