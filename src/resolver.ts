import type {
  AccountResolution,
  FetchLike,
  FetchResponse,
  Jrd,
  WebFingerOptions,
} from "./types.ts";
import { fetchHttpsWithSameHostRedirects } from "./http.ts";
import { expandLrddTemplate, parseHostMetaLinks, selectLrddTemplate, validateLrddTemplate } from "./hostmeta.ts";
import { validateJrd } from "./jrd.ts";
import {
  encodeAcctForQuery,
  normalizeHost,
  tryParseAcct,
} from "./uri.ts";

const DEFAULT_TIMEOUT_MS = 10_000;
const USER_AGENT = "webfinger-guard/1.0";

const platformFetch: FetchLike = (url, init) =>
  fetch(url, init) as unknown as Promise<FetchResponse>;

interface FetchContext {
  readonly fetch: FetchLike;
  readonly signal?: AbortSignal;
}

interface TextResponse {
  readonly text: string;
  readonly url: string;
  readonly redirects: number;
}

function mergeHeaders(
  defaults: Record<string, string>,
  supplied: Record<string, string> | undefined,
): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const [name, value] of Object.entries({
    ...defaults,
    ...(supplied ?? {}),
  })) {
    if (typeof value === "string") {
      headers[name.toLowerCase()] = value;
    }
  }
  return headers;
}

async function fetchText(
  url: string,
  context: FetchContext,
  headers: Record<string, string>,
): Promise<TextResponse | null> {
  const result = await fetchHttpsWithSameHostRedirects(url, {
    fetch: context.fetch,
    maxRedirects: 2,
    headers,
    signal: context.signal,
  });
  if (!result.ok) {
    return null;
  }

  let text: string;
  try {
    text = await result.response.text();
  } catch {
    return null;
  }

  return { text, url: result.url, redirects: result.redirects };
}

function hostMetaUrl(host: string): string {
  return `https://${host}/.well-known/host-meta`;
}

function standardWebFingerUrl(acctUri: string, host: string): string {
  return `https://${host}/.well-known/webfinger?resource=${encodeAcctForQuery(acctUri)}`;
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function timeoutSignal(timeoutMs: number): {
  signal?: AbortSignal;
  clear: () => void;
} {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
    return { clear: () => {} };
  }

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  return {
    signal: controller.signal,
    clear: () => clearTimeout(timer),
  };
}

interface HostMetaDecision {
  readonly template: string | null;
  readonly hasLrddLinks: boolean;
}

function decideFromHostMeta(xml: string, host: string): HostMetaDecision {
  const lrddLinks = parseHostMetaLinks(xml).filter((link) => {
    if (typeof link.rel !== "string") {
      return false;
    }
    return link.rel
      .split(/\s+/)
      .some((token) => token.toLowerCase() === "lrdd");
  });

  if (lrddLinks.length === 0) {
    return { template: null, hasLrddLinks: false };
  }

  for (const link of lrddLinks) {
    if (typeof link.template === "string") {
      const template = validateLrddTemplate(link.template, host);
      if (template) {
        return { template, hasLrddLinks: true };
      }
    }
  }

  return { template: null, hasLrddLinks: true };
}

async function resolveInternal(
  acctInput: string,
  options: WebFingerOptions,
): Promise<AccountResolution | null> {
  const acct = tryParseAcct(acctInput);
  if (!acct) {
    return null;
  }

  const timeout = timeoutSignal(options.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  const context: FetchContext = {
    fetch: options.fetch ?? platformFetch,
    signal: timeout.signal,
  };

  try {
    const webfingerHeaders = mergeHeaders(
      {
        "user-agent": USER_AGENT,
        accept: "application/jrd+json, application/json;q=0.9",
      },
      options.headers,
    );
    const hostMetaHeaders = mergeHeaders(
      {
        "user-agent": USER_AGENT,
        accept: "application/xrd+xml, application/xml;q=0.9",
      },
      options.headers,
    );

    let hostMetaEndpoint: string | undefined;
    let template: string | null = null;

    if (options.lrddTemplate !== undefined) {
      template = validateLrddTemplate(options.lrddTemplate, acct.host);
      if (!template) {
        return null;
      }
    } else if (options.useHostMeta) {
      hostMetaEndpoint = hostMetaUrl(acct.host);
      const document = await fetchText(
        hostMetaEndpoint,
        context,
        hostMetaHeaders,
      );
      if (document) {
        const decision = decideFromHostMeta(document.text, acct.host);
        if (decision.template) {
          template = decision.template;
        } else if (decision.hasLrddLinks) {
          // The host advertised lrdd links but none were safe. Do not silently
          // downgrade to another discovery path.
          return null;
        }
      }
    }

    let endpoint: string;
    if (template) {
      const expanded = expandLrddTemplate(template, acct.uri, acct.host);
      if (!expanded) {
        return null;
      }
      endpoint = expanded;
    } else {
      endpoint = standardWebFingerUrl(acct.uri, acct.host);
    }

    const response = await fetchText(
      endpoint,
      context,
      webfingerHeaders,
    );
    if (!response) {
      return null;
    }

    const parsed = parseJson(response.text);
    const jrd = validateJrd(
      parsed,
      acct.host,
      options.allowedSelfHosts ?? [],
    );
    if (!jrd) {
      return null;
    }

    return {
      acct,
      subject: jrd.subject,
      jrd,
      links: jrd.links ?? [],
      endpoint: response.url,
      redirects: response.redirects,
      ...(hostMetaEndpoint === undefined ? {} : { hostMetaEndpoint }),
    };
  } catch {
    // Fail closed on transport, timeout, and parsing failures.
    return null;
  } finally {
    timeout.clear();
  }
}

/**
 * Resolve an acct URI and return the validated JRD, or null when any security
 * or protocol check fails. The function never emits credentials and never
 * contacts a host other than the one named by the acct URI.
 */
export async function resolveAccount(
  acctInput: string,
  options: WebFingerOptions = {},
): Promise<AccountResolution | null> {
  return resolveInternal(acctInput, options);
}

/**
 * Convenience variant for callers that only need the sanitized JRD document.
 */
export async function resolveWebFinger(
  acctInput: string,
  options: WebFingerOptions = {},
): Promise<Jrd | null> {
  return (await resolveInternal(acctInput, options))?.jrd ?? null;
}

/**
 * Fetch and validate host-meta for an acct URI or bare host. Returns only an
 * lrdd template that is HTTPS, contains `{uri}`, and stays on the query host.
 */
export async function discoverLrddTemplate(
  acctOrHost: string,
  options: WebFingerOptions = {},
): Promise<string | null> {
  let host: string;
  const parsedAcct = tryParseAcct(acctOrHost);
  if (parsedAcct) {
    host = parsedAcct.host;
  } else {
    try {
      host = normalizeHost(acctOrHost);
    } catch {
      return null;
    }
  }

  const timeout = timeoutSignal(options.timeoutMs ?? DEFAULT_TIMEOUT_MS);
  try {
    const document = await fetchText(
      hostMetaUrl(host),
      {
        fetch: options.fetch ?? platformFetch,
        signal: timeout.signal,
      },
      mergeHeaders(
        {
          "user-agent": USER_AGENT,
          accept: "application/xrd+xml, application/xml;q=0.9",
        },
        options.headers,
      ),
    );
    if (!document) {
      return null;
    }
    return selectLrddTemplate(document.text, host);
  } catch {
    return null;
  } finally {
    timeout.clear();
  }
}
