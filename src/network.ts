import type { Simulator } from './simulator.ts';
import type { Random } from './rng.ts';

export interface LinkProfile {
  /** Base one-way latency in ticks. */
  latency: number;
  /** Extra uniform jitter in [0, jitter] ticks, drawn from the seeded RNG. */
  jitter: number;
  /** Probability in [0, 1] that a packet is dropped. */
  lossRate: number;
}

/**
 * Virtual switched network. Every packet draw (loss? jitter?) comes from
 * the single seeded RNG, so a whole run is bit-for-bit reproducible.
 */
export class Network {
  private sim: Simulator;
  private rng: Random;
  private defaultProfile: LinkProfile;
  private links: Map<string, LinkProfile> = new Map();
  private handlers: Map<string, (message: unknown) => void> = new Map();
  readonly stats = { sent: 0, dropped: 0, delivered: 0 };

  constructor(sim: Simulator, rng: Random, defaultProfile?: Partial<LinkProfile>) {
    this.sim = sim;
    this.rng = rng;
    this.defaultProfile = { latency: 1, jitter: 0, lossRate: 0, ...defaultProfile };
  }

  /** Override the profile of one directed link. */
  setLink(from: string, to: string, profile: Partial<LinkProfile>): void {
    this.links.set(`${from}->${to}`, { ...this.defaultProfile, ...profile });
  }

  /** Sever both directions of a link (100% loss). */
  cut(from: string, to: string): void {
    this.setLink(from, to, { lossRate: 1 });
    this.setLink(to, from, { lossRate: 1 });
  }

  /** Restore a link to the default profile. */
  heal(from: string, to: string): void {
    this.links.delete(`${from}->${to}`);
    this.links.delete(`${to}->${from}`);
  }

  /** Attach a delivery handler to an address (virtual switch port). */
  register(address: string, handler: (message: unknown) => void): void {
    this.handlers.set(address, handler);
  }

  /** Send a packet; delivered to the handler registered at `to`. */
  send<T>(from: string, to: string, message: T): void {
    this.stats.sent++;
    const profile = this.links.get(`${from}->${to}`) ?? this.defaultProfile;
    if (this.rng.float() < profile.lossRate) {
      this.stats.dropped++;
      return;
    }
    const handler = this.handlers.get(to);
    if (!handler) throw new Error(`no handler registered for ${to}`);
    const jitter = profile.jitter > 0 ? Math.floor(this.rng.float() * (profile.jitter + 1)) : 0;
    this.sim.schedule(profile.latency + jitter, () => {
      this.stats.delivered++;
      handler(message);
    });
  }
}
