import { canonicalName } from './punyname.ts';

function normalizeHost(host: string): string {
  let h = host.toLowerCase();
  if (h.endsWith('.')) h = h.slice(0, -1);
  if (h.startsWith('[')) return h;
  return canonicalName(h) ?? h;
}

export class BootstrapRegistry {
  readonly publication: string;
  #byTld = new Map<string, string[]>();
  #hosts = new Set<string>();

  private constructor(publication: string) {
    this.publication = publication;
  }

  static fromIANA(json: unknown): BootstrapRegistry {
    if (typeof json !== 'object' || json === null) {
      throw new Error('invalid IANA bootstrap document');
    }
    const doc = json as Record<string, unknown>;
    if (!Array.isArray(doc.services)) {
      throw new Error('IANA bootstrap document missing services');
    }
    const registry = new BootstrapRegistry(
      typeof doc.publication === 'string' ? doc.publication : '',
    );
    for (const service of doc.services) {
      if (!Array.isArray(service) || service.length < 2) continue;
      const [tlds, urls] = service as [unknown, unknown];
      if (!Array.isArray(tlds) || !Array.isArray(urls)) continue;
      const cleanUrls: string[] = [];
      for (const u of urls) {
        if (typeof u !== 'string') continue;
        let parsed: URL;
        try {
          parsed = new URL(u);
        } catch {
          continue;
        }
        if (parsed.protocol !== 'https:') continue;
        cleanUrls.push(u);
        registry.#hosts.add(normalizeHost(parsed.hostname));
      }
      for (const tld of tlds) {
        if (typeof tld !== 'string') continue;
        const canon = canonicalName(tld);
        if (!canon) continue;
        registry.#byTld.set(canon, cleanUrls);
      }
    }
    return registry;
  }

  baseUrlsForName(name: string): string[] {
    const canon = canonicalName(name);
    if (!canon) return [];
    const labels = canon.split('.');
    for (let i = 0; i < labels.length; i++) {
      const urls = this.#byTld.get(labels.slice(i).join('.'));
      if (urls) return urls;
    }
    return [];
  }

  isBootstrapHost(host: string): boolean {
    return this.#hosts.has(normalizeHost(host));
  }

  hosts(): ReadonlySet<string> {
    return this.#hosts;
  }
}
