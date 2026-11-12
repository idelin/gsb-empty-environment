import { Buffer } from "node:buffer";

import type {
  FetchOptions,
  FetchResponse,
  HttpFetcher,
} from "../src/http-fetch.ts";
import type { ParsedHttpUrl } from "../src/url-policy.ts";
import { formatHttpUrl, validateRedirect } from "../src/url-policy.ts";

export interface MockRoute {
  status?: number;
  headers?: Record<string, string>;
  /** Response body. Strings are UTF-8 encoded; Buffers pass through. */
  body?: string | Buffer;
}

export interface MockRequestLog {
  url: string;
  host: string;
  port: number;
  path: string;
  hop: number;
}

/**
 * In-memory replacement for NodeHttpFetcher. Routes are keyed by the exact
 * path-and-query of the requested URL; redirect Location headers are
 * resolved through the same production redirect validator inside the real
 * fetcher logic used by tests indirectly via descriptor fetch options.
 */
export class MockHttpFetcher implements HttpFetcher {
  readonly requests: MockRequestLog[] = [];
  private readonly routes = new Map<string, MockRoute>();
  private readonly failure: Error | undefined;

  constructor(routes: Record<string, MockRoute> = {}, failure?: Error) {
    for (const [path, route] of Object.entries(routes)) {
      this.routes.set(path, route);
    }
    this.failure = failure;
  }

  get(
    url: ParsedHttpUrl,
    anchorHost: string,
    options: FetchOptions,
  ): Promise<FetchResponse> {
    return this.fetchHop(url, anchorHost, options, 0);
  }

  private fetchHop(
    url: ParsedHttpUrl,
    anchorHost: string,
    options: FetchOptions,
    hop: number,
  ): Promise<FetchResponse> {
    if (hop > (options.maxRedirects ?? 5)) {
      return Promise.reject(new Error("too many redirects (mock)"));
    }
    this.requests.push({
      url: formatHttpUrl(url),
      host: url.host,
      port: url.port,
      path: url.path,
      hop,
    });

    if (this.failure !== undefined) {
      return Promise.reject(this.failure);
    }

    const route = this.routes.get(url.path);
    if (route === undefined) {
      return Promise.reject(new Error(`no mock route for ${url.path}`));
    }

    const status = route.status ?? 200;
    const headers = { ...(route.headers ?? {}) };
    if (
      status >= 300 &&
      status < 400 &&
      headers.location !== undefined
    ) {
      // Use the same production validator as NodeHttpFetcher so a mock
      // redirect to another host is rejected with the real error type.
      const next = validateRedirect(headers.location, anchorHost, url);
      return this.fetchHop(next, anchorHost, options, hop + 1);
    }

    const body =
      route.body === undefined
        ? Buffer.alloc(0)
        : typeof route.body === "string"
          ? Buffer.from(route.body, "utf8")
          : route.body;

    if (body.length > options.maxBytes) {
      return Promise.reject(
        new Error(
          `Response body exceeds limit of ${options.maxBytes} bytes (mock)`,
        ),
      );
    }

    return Promise.resolve({ status, headers, body, url: formatHttpUrl(url) });
  }
}
