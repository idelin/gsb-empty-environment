import type { Network } from './network.ts';
import {
  compareFreshness,
  summarize,
  canonicalLsa,
  fnv1a,
  type LSA,
  type LSASummary,
  type LinkEnd,
} from './lsa.ts';

export type Message =
  | { kind: 'update'; lsa: LSA }
  | { kind: 'summary'; summaries: LSASummary[] }
  | { kind: 'request'; origins: string[] };

export class SimNode {
  readonly id: string;
  readonly lsdb = new Map<string, LSA>();
  private readonly net: Network;
  private generation = 0;
  private version = 0;
  private withdrawn = false;
  readonly stats = {
    installed: 0,
    skippedStale: 0,
    summariesSent: 0,
    requestsSent: 0,
    updatesSent: 0,
  };

  constructor(id: string, net: Network) {
    this.id = id;
    this.net = net;
  }

  originate(links: LinkEnd[]): LSA {
    if (this.generation > 0 && !this.withdrawn) {
      throw new Error(`${this.id}: already originated, use updateLinks`);
    }
    if (this.withdrawn) {
      throw new Error(`${this.id}: was withdrawn, must recreate with a new generation`);
    }
    this.generation = 1;
    this.version = 1;
    this.withdrawn = false;
    return this.publish({ origin: this.id, generation: 1, version: 1, withdrawn: false, links });
  }

  updateLinks(links: LinkEnd[]): LSA {
    if (this.generation === 0) throw new Error(`${this.id}: not originated yet`);
    if (this.withdrawn) {
      throw new Error(`${this.id}: withdrawn LSAs cannot be refreshed; recreate with a new generation`);
    }
    this.version += 1;
    return this.publish({
      origin: this.id,
      generation: this.generation,
      version: this.version,
      withdrawn: false,
      links,
    });
  }

  withdraw(): LSA {
    if (this.generation === 0) throw new Error(`${this.id}: not originated yet`);
    if (this.withdrawn) throw new Error(`${this.id}: already withdrawn`);
    this.version += 1;
    this.withdrawn = true;
    return this.publish({
      origin: this.id,
      generation: this.generation,
      version: this.version,
      withdrawn: true,
      links: [],
    });
  }

  recreate(links: LinkEnd[]): LSA {
    if (!this.withdrawn) throw new Error(`${this.id}: recreate requires a prior withdraw`);
    this.generation += 1;
    this.version = 1;
    this.withdrawn = false;
    return this.publish({
      origin: this.id,
      generation: this.generation,
      version: this.version,
      withdrawn: false,
      links,
    });
  }

  private publish(lsa: LSA): LSA {
    this.lsdb.set(this.id, lsa);
    for (const n of this.net.upNeighbors(this.id)) {
      this.sendUpdate(n, lsa);
    }
    return lsa;
  }

  private sendUpdate(to: string, lsa: LSA): void {
    this.stats.updatesSent++;
    this.net.send(this.id, to, { kind: 'update', lsa });
  }

  sendSummaryTo(neighbor: string): void {
    this.stats.summariesSent++;
    const summaries = [...this.lsdb.values()].map(summarize);
    this.net.send(this.id, neighbor, { kind: 'summary', summaries });
  }

  receive(msg: Message, from: string): void {
    switch (msg.kind) {
      case 'update':
        this.onUpdate(msg.lsa, from);
        break;
      case 'summary':
        this.onSummary(msg.summaries, from);
        break;
      case 'request':
        this.onRequest(msg.origins, from);
        break;
    }
  }

  private onUpdate(lsa: LSA, from: string): void {
    if (!this.install(lsa)) return;
    for (const n of this.net.upNeighbors(this.id)) {
      if (n !== from) this.sendUpdate(n, lsa);
    }
  }

  install(lsa: LSA): boolean {
    const current = this.lsdb.get(lsa.origin);
    if (current && compareFreshness(lsa, current) <= 0) {
      this.stats.skippedStale++;
      return false;
    }
    this.lsdb.set(lsa.origin, lsa);
    this.stats.installed++;
    return true;
  }

  private onSummary(summaries: LSASummary[], from: string): void {
    const theirs = new Map(summaries.map((s) => [s.origin, s]));
    const want: string[] = [];
    for (const s of summaries) {
      const mine = this.lsdb.get(s.origin);
      if (!mine || compareFreshness(s, mine) > 0) want.push(s.origin);
    }
    for (const [origin, lsa] of this.lsdb) {
      const s = theirs.get(origin);
      if (!s || compareFreshness(lsa, s) > 0) this.sendUpdate(from, lsa);
    }
    if (want.length > 0) {
      this.stats.requestsSent++;
      this.net.send(this.id, from, { kind: 'request', origins: want });
    }
  }

  private onRequest(origins: string[], from: string): void {
    for (const origin of origins) {
      const lsa = this.lsdb.get(origin);
      if (lsa) this.sendUpdate(from, lsa);
    }
  }

  dbDigest(): string {
    const dump = [...this.lsdb.values()]
      .map(canonicalLsa)
      .sort()
      .join('\n');
    return fnv1a(dump);
  }
}
