import type { FetchLike, FetchResponse } from "./types.ts";
import { hostOfUrl, tryToHttpsUrl } from "./uri.ts";

const REDIRECT_STATUSES = new Set([301, 302, 303, 307, 308]);

export type SecureFetchFailure =
  | "invalid_url"
  | "redirect_limit"
  | "cross_host_redirect"
  | "missing_location"
  | "unexpected_status";

export type SecureFetchResult =
  | {
      readonly ok: true;
      readonly response: FetchResponse;
      readonly url: string;
      readonly redirects: number;
    }
  | {
      readonly ok: false;
      readonly reason: SecureFetchFailure;
      readonly status?: number;
    };

export interface SecureFetchOptions {
  readonly fetch: FetchLike;
  readonly maxRedirects?: number;
  readonly headers?: Record<string, string>;
  readonly signal?: AbortSignal;
}

/**
 * Fetch an HTTPS URL, following redirects only while every hop stays on the
 * original host. The redirect budget is capped at two, regardless of caller
 * configuration.
 */
export async function fetchHttpsWithSameHostRedirects(
  rawUrl: string | URL,
  options: SecureFetchOptions,
): Promise<SecureFetchResult> {
  const initial = tryToHttpsUrl(typeof rawUrl === "string" ? rawUrl : rawUrl.href);
  if (!initial) {
    return { ok: false, reason: "invalid_url" };
  }

  const requestedMax = options.maxRedirects ?? 2;
  const maxRedirects = Math.max(0, Math.min(2, Math.trunc(requestedMax)));
  const expectedHost = hostOfUrl(initial);
  let current = initial;
  let redirects = 0;

  for (;;) {
    const response = await options.fetch(current.href, {
      method: "GET",
      redirect: "manual",
      headers: options.headers,
      signal: options.signal,
    });

    if (!REDIRECT_STATUSES.has(response.status)) {
      if (response.status < 200 || response.status >= 300) {
        return {
          ok: false,
          reason: "unexpected_status",
          status: response.status,
        };
      }
      return {
        ok: true,
        response,
        url: current.href,
        redirects,
      };
    }

    if (redirects >= maxRedirects) {
      return { ok: false, reason: "redirect_limit", status: response.status };
    }

    const location = response.headers.get("location");
    if (!location) {
      return { ok: false, reason: "missing_location", status: response.status };
    }

    const next = tryToHttpsUrl(location, current.href);
    if (!next || hostOfUrl(next) !== expectedHost) {
      return {
        ok: false,
        reason: "cross_host_redirect",
        status: response.status,
      };
    }

    redirects += 1;
    current = next;
  }
}
