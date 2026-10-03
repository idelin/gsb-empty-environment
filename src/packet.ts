/**
 * Well-known DSCP codepoints (RFC 2474 / RFC 2597 / RFC 3246).
 * The DSCP field is the high 6 bits of the IPv4 TOS / IPv6 Traffic Class byte.
 */
export const DSCP = {
  CS0: 0,
  AF11: 10,
  AF12: 12,
  AF13: 14,
  AF21: 18,
  AF22: 20,
  AF23: 22,
  AF31: 26,
  AF32: 28,
  AF33: 30,
  AF41: 34,
  AF42: 36,
  AF43: 38,
  CS5: 40,
  EF: 46,
  CS6: 48,
  CS7: 56,
} as const;

/** Internal forwarding queues of the DiffServ node. */
export type QueueId = 'EF' | 'AF4' | 'AF3' | 'AF2' | 'AF1' | 'BE';

export const QUEUE_IDS: readonly QueueId[] = ['EF', 'AF4', 'AF3', 'AF2', 'AF1', 'BE'];

/** A single IP packet as seen by the forwarder. Times are in seconds. */
export interface Packet {
  id: number;
  /** 6-bit DSCP value taken from the IP header TOS/Traffic-Class byte. */
  dscp: number;
  sizeBytes: number;
  /** Simulation time (seconds) at which the packet enters the node. */
  arrivalTime: number;
  /** Optional flow identifier used for per-flow statistics. */
  flowId?: string;
}
