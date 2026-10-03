import { DSCP } from './packet.ts';
import type { Packet, QueueId } from './packet.ts';

function defaultTable(): Array<[number, QueueId]> {
  return [
    [DSCP.EF, 'EF'],
    [DSCP.AF41, 'AF4'],
    [DSCP.AF42, 'AF4'],
    [DSCP.AF43, 'AF4'],
    [DSCP.AF31, 'AF3'],
    [DSCP.AF32, 'AF3'],
    [DSCP.AF33, 'AF3'],
    [DSCP.AF21, 'AF2'],
    [DSCP.AF22, 'AF2'],
    [DSCP.AF23, 'AF2'],
    [DSCP.AF11, 'AF1'],
    [DSCP.AF12, 'AF1'],
    [DSCP.AF13, 'AF1'],
    [DSCP.CS0, 'BE'],
  ];
}

/**
 * Maps the DSCP/TOS field of an IP header onto an internal forwarding queue:
 *   EF (46)      -> expedited forwarding, strict priority
 *   AFxy         -> assured forwarding class x (drop precedence y ignored here)
 *   anything else-> best effort
 * The table can be extended/overridden per deployment.
 */
export class DscpClassifier {
  private readonly table: Map<number, QueueId>;

  constructor(overrides?: ReadonlyMap<number, QueueId>) {
    this.table = new Map(defaultTable());
    if (overrides) {
      for (const [dscp, queue] of overrides) {
        DscpClassifier.assertDscp(dscp);
        this.table.set(dscp, queue);
      }
    }
  }

  private static assertDscp(dscp: number): void {
    if (!Number.isInteger(dscp) || dscp < 0 || dscp > 63) {
      throw new RangeError(`DSCP must be an integer in [0, 63], got ${dscp}`);
    }
  }

  /** Classify by the 6-bit DSCP value. Unknown codepoints fall back to BE. */
  classifyDscp(dscp: number): QueueId {
    DscpClassifier.assertDscp(dscp);
    return this.table.get(dscp) ?? 'BE';
  }

  /**
   * Classify by the raw IPv4 TOS / IPv6 Traffic-Class byte.
   * DSCP occupies the high 6 bits; the low 2 bits are ECN and are ignored.
   */
  classifyTos(tosByte: number): QueueId {
    if (!Number.isInteger(tosByte) || tosByte < 0 || tosByte > 255) {
      throw new RangeError(`TOS byte must be an integer in [0, 255], got ${tosByte}`);
    }
    return this.classifyDscp(tosByte >> 2);
  }

  classify(packet: Packet): QueueId {
    return this.classifyDscp(packet.dscp);
  }
}
