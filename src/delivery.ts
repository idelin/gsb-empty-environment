/**
 * Delivers queued reports to declared Reporting-Endpoints via POST, per the
 * Reporting API: JSON array body, application/reports+json content type, no
 * credentials, at most 100 reports per delivery.
 */

export const MAX_REPORTS_PER_DELIVERY = 100;

export interface QueuedReport {
  type: string;
  url: string;
  /** Name of the endpoint group this report is assigned to. */
  endpoint: string;
  body: Record<string, unknown>;
}

export interface DeliveryAttempt {
  endpoint: string;
  url: string;
  reportCount: number;
  ok: boolean;
  status?: number;
  error?: unknown;
}

export interface DeliverOptions {
  /** Endpoint name to URL map, as produced by parseReportingEndpoints. */
  endpoints: ReadonlyMap<string, string>;
  reports: readonly QueuedReport[];
  /** Injectable for testing; defaults to the global fetch. */
  fetchImpl?: typeof fetch;
  maxBatchSize?: number;
}

export async function deliverReports(
  options: DeliverOptions,
): Promise<DeliveryAttempt[]> {
  const fetchImpl = options.fetchImpl ?? fetch;
  const maxBatchSize = options.maxBatchSize ?? MAX_REPORTS_PER_DELIVERY;

  const byEndpoint = new Map<string, QueuedReport[]>();
  for (const report of options.reports) {
    if (!options.endpoints.has(report.endpoint)) continue; // undeclared: drop
    const list = byEndpoint.get(report.endpoint);
    if (list) {
      list.push(report);
    } else {
      byEndpoint.set(report.endpoint, [report]);
    }
  }

  const attempts: DeliveryAttempt[] = [];
  for (const [endpoint, reports] of byEndpoint) {
    const url = options.endpoints.get(endpoint)!;
    for (let i = 0; i < reports.length; i += maxBatchSize) {
      const batch = reports.slice(i, i + maxBatchSize);
      const payload = batch.map(({ type, url: reportUrl, body }) => ({
        type,
        url: reportUrl,
        body,
      }));
      // Credentials are never attached: no Cookie, no Authorization.
      const headers = new Headers();
      headers.set("content-type", "application/reports+json");
      try {
        const response = await fetchImpl(url, {
          method: "POST",
          headers,
          body: JSON.stringify(payload),
          credentials: "omit",
        });
        attempts.push({
          endpoint,
          url,
          reportCount: batch.length,
          ok: response.ok,
          status: response.status,
        });
      } catch (error) {
        attempts.push({ endpoint, url, reportCount: batch.length, ok: false, error });
      }
    }
  }
  return attempts;
}
