import { classifyHost, type AddressSpace } from "./ip-address.ts";
import { parseReportingEndpoints } from "./reporting-endpoints.ts";

export interface Report {
  type: string;
  url: string;
  body: Record<string, unknown>;
}

export interface OutgoingRequest {
  url: string;
  method: "POST";
  headers: Record<string, string>;
  body: string;
}

export interface TransportResponse {
  status: number;
  headers?: Record<string, string | string[] | undefined>;
}

/** Actual network send. Must not attach ambient credentials. */
export type Transport = (request: OutgoingRequest) => Promise<TransportResponse>;

/** DNS resolution for a hostname; returns all A/AAAA records. */
export type Resolver = (hostname: string) => Promise<string[]>;

export type DeliveryStatus = "delivered" | "blocked" | "failed";

export interface DeliveryResult {
  origin: string;
  group: string;
  delivered: number;
  dropped: number;
  status: DeliveryStatus;
  reason?: string;
}

export class BlockedError extends Error {}
export class DeliveryError extends Error {}

export interface ReportDeliveryOptions {
  /** URL of the document these reports belong to. */
  documentUrl: string | URL;
  resolver: Resolver;
  transport: Transport;
  maxRedirects?: number;
  /** Max reports per POST body; overflow is dropped per origin. */
  maxReportsPerDelivery?: number;
}

interface QueueEntry {
  origin: string;
  group: string;
  report: Report;
}

function getHeader(
  headers: Record<string, string | string[] | undefined> | undefined,
  name: string,
): string | undefined {
  if (!headers) return undefined;
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === name) {
      const v = headers[key];
      return Array.isArray(v) ? v[0] : v;
    }
  }
  return undefined;
}

/**
 * Queues reports per source origin and delivers them to the document's
 * Reporting-Endpoints. Every delivery target, including every redirect
 * hop, is validated against the private-address rules before any bytes
 * are sent: if the host is (or resolves to) any loopback, private or
 * link-local address, the delivery is blocked unless the document
 * itself lives in the same address space.
 *
 * Requests carry no credentials: the only header sent is
 * Content-Type: application/reports+json.
 */
export class ReportDeliveryService {
  #documentUrl: URL;
  #resolver: Resolver;
  #transport: Transport;
  #maxRedirects: number;
  #maxReports: number;
  #endpoints = new Map<string, URL>();
  #queues = new Map<string, QueueEntry[]>();
  #documentSpace: Promise<AddressSpace> | null = null;

  constructor(options: ReportDeliveryOptions) {
    this.#documentUrl = new URL(options.documentUrl);
    this.#resolver = options.resolver;
    this.#transport = options.transport;
    this.#maxRedirects = options.maxRedirects ?? 5;
    this.#maxReports = options.maxReportsPerDelivery ?? 100;
  }

  /** Configure endpoints from Reporting-Endpoints header field values. */
  setEndpoints(fieldValues: string[], responseUrl: string | URL = this.#documentUrl): void {
    this.#endpoints = parseReportingEndpoints(fieldValues, responseUrl);
  }

  endpointUrl(group: string): URL | undefined {
    return this.#endpoints.get(group);
  }

  /** Queue reports from a source origin for an endpoint group. */
  enqueue(origin: string, group: string, reports: Report[]): void {
    const key = `${origin} ${group}`;
    let queue = this.#queues.get(key);
    if (!queue) {
      queue = [];
      this.#queues.set(key, queue);
    }
    for (const report of reports) queue.push({ origin, group, report });
  }

  /**
   * Deliver all queued reports. Each (origin, group) queue is sent in
   * batches of at most maxReportsPerDelivery; overflow is dropped for
   * that queue only and never affects other origins' queues.
   */
  async flush(): Promise<DeliveryResult[]> {
    const results: DeliveryResult[] = [];
    for (const [key, queue] of this.#queues) {
      this.#queues.delete(key);
      if (queue.length === 0) continue;
      const { origin, group } = queue[0];
      const endpoint = this.#endpoints.get(group);
      if (!endpoint) {
        results.push({
          origin,
          group,
          delivered: 0,
          dropped: queue.length,
          status: "failed",
          reason: "unknown-endpoint-group",
        });
        continue;
      }
      const batch = queue.slice(0, this.#maxReports);
      const overflow = queue.length - batch.length;
      try {
        const response = await this.#send(
          endpoint,
          JSON.stringify(batch.map((entry) => entry.report)),
        );
        const ok = response.status >= 200 && response.status < 300;
        results.push({
          origin,
          group,
          delivered: ok ? batch.length : 0,
          dropped: overflow + (ok ? 0 : batch.length),
          status: ok ? "delivered" : "failed",
          reason: ok ? undefined : `http-${response.status}`,
        });
      } catch (err) {
        const blocked = err instanceof BlockedError;
        results.push({
          origin,
          group,
          delivered: 0,
          dropped: overflow + batch.length,
          status: blocked ? "blocked" : "failed",
          reason: err instanceof Error ? err.message : String(err),
        });
      }
      void key;
    }
    return results;
  }

  async #send(url: URL, body: string): Promise<TransportResponse> {
    let current = url;
    for (let redirects = 0; ; redirects++) {
      await this.#assertAllowed(current);
      const response = await this.#transport({
        url: current.href,
        method: "POST",
        headers: { "content-type": "application/reports+json" },
        body,
      });
      const location = getHeader(response.headers, "location");
      if (response.status >= 300 && response.status < 400 && location !== undefined) {
        if (redirects >= this.#maxRedirects) {
          throw new DeliveryError("too-many-redirects");
        }
        let next: URL;
        try {
          next = new URL(location, current);
        } catch {
          throw new DeliveryError("invalid-redirect-location");
        }
        current = next;
        continue;
      }
      return response;
    }
  }

  async #assertAllowed(url: URL): Promise<void> {
    if (url.protocol !== "https:") {
      throw new BlockedError(`scheme-not-https: ${url.protocol}`);
    }
    if (url.username !== "" || url.password !== "") {
      throw new BlockedError("userinfo-not-allowed");
    }
    let spaces: AddressSpace[];
    const literal = classifyHost(url.hostname);
    if (literal !== null) {
      spaces = [literal];
    } else {
      const ips = await this.#resolver(url.hostname);
      if (ips.length === 0) {
        throw new BlockedError(`host-unresolvable: ${url.hostname}`);
      }
      spaces = ips.map((ip) => {
        const space = classifyHost(ip);
        if (space === null) {
          throw new BlockedError(`unparseable-resolved-address: ${ip}`);
        }
        return space;
      });
    }
    const documentSpace = await this.#documentAddressSpace();
    for (const space of spaces) {
      if (space !== "public" && space !== documentSpace) {
        throw new BlockedError(
          `blocked-${space}-address: ${url.hostname} (document is ${documentSpace})`,
        );
      }
    }
  }

  #documentAddressSpace(): Promise<AddressSpace> {
    if (this.#documentSpace === null) {
      this.#documentSpace = (async () => {
        const literal = classifyHost(this.#documentUrl.hostname);
        if (literal !== null) return literal;
        let ips: string[];
        try {
          ips = await this.#resolver(this.#documentUrl.hostname);
        } catch {
          return "public";
        }
        const spaces = new Set<AddressSpace>();
        for (const ip of ips) spaces.add(classifyHost(ip) ?? "public");
        // The same-space exception only applies when the document is
        // unambiguously inside one non-public address space.
        if (spaces.size === 1) return [...spaces][0];
        return "public";
      })();
    }
    return this.#documentSpace;
  }
}
