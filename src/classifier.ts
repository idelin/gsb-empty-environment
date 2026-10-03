import { DSCP, type QueueId } from './packet.ts';

/**
 * Map a 6-bit DSCP value to a forwarding queue (PHB):
 *  - DSCP 46 (EF)            -> 'EF'  expedited forwarding, strict priority
 *  - DSCP 8x+2y (AFxy)       -> 'AF1'..'AF4' assured forwarding classes
 *  - everything else         -> 'BE'  best effort
 */
export function classifyDscp(dscp: number): QueueId {
  if (!Number.isInteger(dscp) || dscp < 0 || dscp > 0x3f) {
    throw new RangeError(`invalid DSCP: ${dscp}`);
  }
  if (dscp === DSCP.EF) return 'EF';

  const afClass = dscp >> 3;        // high 3 bits: AF class 1..4
  const dropBits = dscp & 0x7;      // low 3 bits: 2 * drop precedence
  if (afClass >= 1 && afClass <= 4 && (dropBits === 2 || dropBits === 4 || dropBits === 6)) {
    return `AF${afClass}` as QueueId;
  }
  return 'BE';
}

/** Classify a packet directly from the raw IP TOS byte. */
export function classifyTosByte(tosByte: number): QueueId {
  return classifyDscp((tosByte >> 2) & 0x3f);
}
