/**
 * Optional DnsResolver backed by node:dns. Inject this in real delivery
 * components; tests can supply a fake resolver.
 */

import { resolveTxt } from "node:dns/promises";
import type { DnsResolver } from "./client.ts";

export class NodeDnsResolver implements DnsResolver {
  async resolveStsTxt(domain: string): Promise<string[]> {
    const records = await resolveTxt(`_mta-sts.${domain}`);
    return records.map((chunks) => chunks.join(""));
  }
}
