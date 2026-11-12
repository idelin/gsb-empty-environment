/**
 * HTTP transport for descriptor retrieval.
 *
 * The transport sits behind the `HttpFetcher` interface so the security
 * layer can be exercised with deterministic in-memory responses (and so the
 * package is testable in sandboxes without socket access). The production
 * implementation uses node:http only:
 *
 *  - redirects are followed manually so every hop can be checked against
 *    the anchor host before a new connection is made;
 *  - response bodies are streamed through a counting guard that aborts the
 *    socket as soon as the configured XML size limit is exceeded, so an
 *    oversized descriptor is never fully buffered;
 *  - no credentials, cookies or connection pools cross host boundaries.
 */

import { Agent, request as httpRequest, type IncomingMessage } from "node:http";
import { Buffer } from "node:buffer";
import type { ClientRequest, RequestOptions } from "node:http";

import {
  BadResponseError,
  HttpStatusError,
  TooManyRedirectsError,
  XmlTooLargeError,
} from "./errors.ts";
import {
  formatHttpUrl,
  parseHttpUrl,
  validateRedirect,
  type ParsedHttpUrl,
} from "./url-policy.ts";

export interface FetchResponse {
  status: number;
  headers: Record<string, string>;
  /** Full response body, guaranteed to be at most `maxBytes` long. */
  body: Buffer;
  /** Final URL after all validated redirect hops. */
  url: string;
}

export interface FetchOptions {
  /** Per-hop socket idle timeout in milliseconds. */
  timeoutMs?: number;
  /** Maximum accepted body size in bytes. */
  maxBytes: number;
  /** Maximum redirect chain length. */
  maxRedirects?: number;
  /** Extra request headers, e.g. a descriptive User-Agent. */
  headers?: Record<string, string>;
}

export interface HttpFetcher {
  get(
    url: ParsedHttpUrl,
    anchorHost: string,
    options: FetchOptions,
  ): Promise<FetchResponse>;
}

const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

/** Signature of node:http's request() used by the fetcher. */
export type HttpRequestFn = (
  options: RequestOptions,
  callback: (response: IncomingMessage) => void,
) => ClientRequest;

function headerValue(message: IncomingMessage, name: string): string | undefined {
  const raw = message.headers[name.toLowerCase()];
  if (Array.isArray(raw)) {
    return raw[0];
  }
  return raw;
}

function readBoundedBody(
  message: IncomingMessage,
  maxBytes: number,
): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let received = 0;
    let settled = false;

    const declaredLength = Number(message.headers["content-length"]);
    if (
      Number.isFinite(declaredLength) &&
      declaredLength > maxBytes
    ) {
      message.destroy();
      settled = true;
      reject(
        new XmlTooLargeError(
          `Content-Length ${declaredLength} exceeds limit of ${maxBytes} bytes`,
        ),
      );
      return;
    }

    message.on("data", (chunk: Buffer) => {
      if (settled) {
        return;
      }
      received += chunk.length;
      if (received > maxBytes) {
        settled = true;
        message.destroy();
        reject(
          new XmlTooLargeError(
            `Response body exceeds limit of ${maxBytes} bytes`,
          ),
        );
        return;
      }
      chunks.push(chunk);
    });
    message.on("end", () => resolve(Buffer.concat(chunks)));
    message.on("error", (error: Error) => {
      if (settled) {
        return;
      }
      settled = true;
      if (received > maxBytes) {
        reject(
          new XmlTooLargeError(
            `Response body exceeds limit of ${maxBytes} bytes`,
          ),
        );
        return;
      }
      reject(new BadResponseError(`Socket error while reading body: ${error.message}`));
    });
  });
}

/**
 * Production fetcher backed by node:http. Each call gets a fresh agent so
 * no keep-alive connection can be reused across (rejected) hosts.
 */
export class NodeHttpFetcher implements HttpFetcher {
  private readonly requestFn: HttpRequestFn;

  /**
   * @param requestFn Injection point for the HTTP request function; tests
   *   supply a fake while production callers use node:http's default.
   */
  constructor(requestFn: HttpRequestFn = httpRequest) {
    this.requestFn = requestFn;
  }

  get(
    url: ParsedHttpUrl,
    anchorHost: string,
    options: FetchOptions,
  ): Promise<FetchResponse> {
    const timeoutMs = options.timeoutMs ?? 5000;
    const maxRedirects = options.maxRedirects ?? 5;
    return this.fetchHop(url, anchorHost, options, timeoutMs, maxRedirects, 0);
  }

  private fetchHop(
    url: ParsedHttpUrl,
    anchorHost: string,
    options: FetchOptions,
    timeoutMs: number,
    maxRedirects: number,
    hop: number,
  ): Promise<FetchResponse> {
    if (hop > maxRedirects) {
      return Promise.reject(
        new TooManyRedirectsError(
          `Redirect chain exceeds limit of ${maxRedirects} hops`,
        ),
      );
    }

    return new Promise<FetchResponse>((resolve, reject) => {
      let settled = false;
      const failOnce = (error: Error): void => {
        if (!settled) {
          settled = true;
          reject(error);
        }
      };
      const succeedOnce = (value: FetchResponse): void => {
        if (!settled) {
          settled = true;
          resolve(value);
        }
      };
      const agent = new Agent({ keepAlive: false });
      const req = this.requestFn(
        {
          protocol: "http:",
          hostname: url.host,
          port: url.port,
          path: url.path,
          method: "GET",
          agent,
          timeout: timeoutMs,
          headers: {
            Accept: "text/xml, application/xml",
            "Cache-Control": "no-store",
            ...(options.headers ?? {}),
          },
        },
        (message) => {
          const status = message.statusCode ?? 0;

          if (REDIRECT_STATUSES.has(status)) {
            message.resume();
            const location = headerValue(message, "location");
            if (location === undefined) {
              message.destroy();
              failOnce(
                new BadResponseError(
                  `HTTP ${status} redirect without a Location header`,
                ),
              );
              return;
            }
            let next: ParsedHttpUrl;
            try {
              next = validateRedirect(location, anchorHost, url);
            } catch (error) {
              message.destroy();
              failOnce(error as Error);
              return;
            }
            this.fetchHop(
              next,
              anchorHost,
              options,
              timeoutMs,
              maxRedirects,
              hop + 1,
            ).then(succeedOnce, failOnce);
            return;
          }

          if (status < 200 || status >= 300) {
            message.resume();
            message.on("end", () =>
              failOnce(
                new HttpStatusError(
                  status,
                  `Device description request failed with HTTP ${status}`,
                ),
              ),
            );
            message.on("error", () =>
              failOnce(
                new HttpStatusError(
                  status,
                  `Device description request failed with HTTP ${status}`,
                ),
              ),
            );
            return;
          }

          readBoundedBody(message, options.maxBytes).then(
            (body) =>
              succeedOnce({
                status,
                headers: flattenHeaders(message),
                body,
                url: formatHttpUrl(url),
              }),
            failOnce,
          );
        },
      );

      req.on("timeout", () => {
        req.destroy();
        failOnce(
          new BadResponseError(`Request to ${formatHttpUrl(url)} timed out`),
        );
      });
      req.on("error", (error: Error) => {
        failOnce(
          new BadResponseError(`HTTP request failed: ${error.message}`),
        );
      });
    });
  }
}

function flattenHeaders(message: IncomingMessage): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(message.headers)) {
    if (typeof value === "string") {
      result[key] = value;
    } else if (Array.isArray(value) && value.length > 0) {
      result[key] = value.join(", ");
    }
  }
  return result;
}

/**
 * Re-exports for callers that build mock fetchers: lets tests construct the
 * same URL value the production code would request.
 */
export { parseHttpUrl, formatHttpUrl };
