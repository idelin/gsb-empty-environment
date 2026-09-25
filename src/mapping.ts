import type { AnonymizeError, JsonValue, MappingExport } from "./types.ts";
import { Pseudonymizer } from "./pseudonym.ts";

interface MutableEntry {
  pseudonym: string;
  valueDigest: string;
  occurrences: string[];
}

export class MappingTable {
  readonly #pseudonymizer: Pseudonymizer;
  readonly #domains = new Map<string, Map<string, MutableEntry>>();
  readonly #byPseudonym = new Map<string, Map<string, string>>();
  readonly collisions: AnonymizeError[] = [];

  constructor(pseudonymizer: Pseudonymizer) {
    this.#pseudonymizer = pseudonymizer;
  }

  register(domain: string, value: JsonValue, path: string): string {
    const valueDigest = this.#pseudonymizer.digest(domain, value);
    const pseudonym = this.#pseudonymizer.pseudonym(domain, value);

    let domainMap = this.#domains.get(domain);
    if (!domainMap) {
      domainMap = new Map();
      this.#domains.set(domain, domainMap);
    }
    const existing = domainMap.get(valueDigest);
    if (existing) {
      existing.occurrences.push(path);
    } else {
      domainMap.set(valueDigest, { pseudonym, valueDigest, occurrences: [path] });
    }

    let reverse = this.#byPseudonym.get(domain);
    if (!reverse) {
      reverse = new Map();
      this.#byPseudonym.set(domain, reverse);
    }
    const ownerDigest = reverse.get(pseudonym);
    if (ownerDigest === undefined) {
      reverse.set(pseudonym, valueDigest);
    } else if (ownerDigest !== valueDigest) {
      this.collisions.push({
        code: "COLLISION",
        path,
        message:
          `Pseudonym collision in domain "${domain}" at ${path}: ` +
          `distinct values produced the same pseudonym ${pseudonym}`,
      });
    }
    return pseudonym;
  }

  export(): MappingExport {
    const domains = [...this.#domains.entries()]
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([domain, entries]) => ({
        domain,
        entries: [...entries.values()]
          .map((e) => ({
            pseudonym: e.pseudonym,
            valueDigest: e.valueDigest,
            occurrences: [...e.occurrences].sort(),
          }))
          .sort((a, b) => (a.pseudonym < b.pseudonym ? -1 : 1)),
      }));
    return { version: 1, domains, collisions: [...this.collisions] };
  }
}
