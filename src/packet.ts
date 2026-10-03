export const DSCP = {
  BE: 0,
  EF: 46,
  AF11: 10, AF12: 12, AF13: 14,
  AF21: 18, AF22: 20, AF23: 22,
  AF31: 26, AF32: 28, AF33: 30,
  AF41: 34, AF42: 36, AF43: 38,
} as const;

export type QueueId = 'EF' | 'AF1' | 'AF2' | 'AF3' | 'AF4' | 'BE';

export interface Packet {
  id: number;
  /** 6-bit DSCP value carried in the IP header TOS byte. */
  dscp: number;
  sizeBytes: number;
  /** Arrival time in milliseconds (simulation clock). */
  arrivalMs: number;
  flowId: string;
}

/** Extract the 6-bit DSCP from the 8-bit IPv4 TOS / IPv6 Traffic Class byte. */
export function dscpFromTos(tosByte: number): number {
  if (!Number.isInteger(tosByte) || tosByte < 0 || tosByte > 0xff) {
    throw new RangeError(`invalid TOS byte: ${tosByte}`);
  }
  return (tosByte >> 2) & 0x3f;
}

/** Build the TOS byte for a given DSCP (ECN bits left zeroed). */
export function tosFromDscp(dscp: number): number {
  if (!Number.isInteger(dscp) || dscp < 0 || dscp > 0x3f) {
    throw new RangeError(`invalid DSCP: ${dscp}`);
  }
  return (dscp << 2) & 0xff;
}
