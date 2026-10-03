import type { Rng } from './rng.ts';

export interface LinkProfile {
  /** Packet loss probability in [0, 1]. */
  lossRate: number;
  /** Base one-way latency in ticks. */
  baseLatency: number;
  /** Extra uniform jitter in [0, jitter] ticks. */
  jitter: number;
}

export const DEFAULT_LINK: LinkProfile = { lossRate: 0, baseLatency: 1, jitter: 0 };

/**
 * Virtual switch. Every frame is routed through here; loss and latency are
 * drawn from the simulator's single seeded RNG, so a whole run replays
 * bit-for-bit from its seed.
 */
export class VirtualSwitch {
  private links = new Map<string, LinkProfile>();
  private partitioned = new Set<string>();
  private rng: Rng;
  private defaultProfile: LinkProfile;

  constructor(rng: Rng, defaultProfile: LinkProfile = DEFAULT_LINK) {
    this.rng = rng;
    this.defaultProfile = defaultProfile;
  }

  static key(a: string, b: string): string {
    return a < b ? `${a}|${b}` : `${b}|${a}`;
  }

  setLink(a: string, b: string, profile: LinkProfile): void {
    this.links.set(VirtualSwitch.key(a, b), profile);
  }

  /** Hard partition: 100% loss in both directions until healed. */
  isolate(a: string, b: string): void {
    this.partitioned.add(VirtualSwitch.key(a, b));
  }

  heal(a: string, b: string): void {
    this.partitioned.delete(VirtualSwitch.key(a, b));
  }

  /** Cut one node off from a set of peers. */
  isolateFrom(node: string, others: readonly string[]): void {
    for (const other of others) this.isolate(node, other);
  }

  healFrom(node: string, others: readonly string[]): void {
    for (const other of others) this.heal(node, other);
  }

  /**
   * Route one frame. Returns the delivery delay in ticks, or null if the
   * frame is dropped (loss draw or partition).
   */
  route(from: string, to: string): number | null {
    if (this.partitioned.has(VirtualSwitch.key(from, to))) return null;
    const profile = this.links.get(VirtualSwitch.key(from, to)) ?? this.defaultProfile;
    if (profile.lossRate > 0 && this.rng.next() < profile.lossRate) return null;
    const jitter = profile.jitter > 0 ? this.rng.int(profile.jitter + 1) : 0;
    return profile.baseLatency + jitter;
  }
}
