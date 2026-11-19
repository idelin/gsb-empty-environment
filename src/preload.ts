import {
  cspAllows,
  parseCspHeaders,
  type CspPolicy,
  type FetchDirective,
} from "./csp.ts";

export type PreloadAs = "script" | "style" | "image" | "font";

export type CrossOriginMode = "anonymous" | "use-credentials";

export interface PreloadHint {
  url: string;
  as: PreloadAs;
  crossOrigin: CrossOriginMode | null;
  integrity: string | null;
  /** Timestamp (ms) when the hint was recorded. */
  recordedAt: number;
}

/** Attributes of the page tag that will consume the preloaded resource. */
export interface TagRequest {
  url: string;
  as: PreloadAs;
  crossOrigin: CrossOriginMode | null;
  integrity: string | null;
}

export interface FinalResponseInfo {
  status: number;
  /** All Content-Security-Policy header values on the final response. */
  cspHeaders?: Array<string | null | undefined>;
}

export type ReuseDecision =
  | { reuse: true; hint: PreloadHint }
  | {
      reuse: false;
      reason:
        | "no-hint"
        | "expired"
        | "csp-blocked"
        | "crossorigin-mismatch"
        | "integrity-mismatch";
    };

export const PRELOAD_HINT_TTL_MS = 30_000;

const AS_TO_DIRECTIVE: Record<PreloadAs, FetchDirective> = {
  script: "script-src",
  style: "style-src",
  image: "img-src",
  font: "font-src",
};

export function directiveForAs(as: PreloadAs): FetchDirective {
  return AS_TO_DIRECTIVE[as];
}

/**
 * Parse the preload hints out of a 103 Early Hints `Link` header value.
 * Only `rel=preload` links with a known `as` value are recorded.
 */
export function parseEarlyHintLinks(
  linkHeader: string,
  baseUrl: string,
  now: number = Date.now(),
): PreloadHint[] {
  const hints: PreloadHint[] = [];
  // Split on commas that are not inside quoted strings or angle brackets.
  const parts: string[] = [];
  let depth = 0;
  let inQuote = false;
  let current = "";
  for (const ch of linkHeader) {
    if (ch === '"') inQuote = !inQuote;
    if (!inQuote) {
      if (ch === "<") depth++;
      if (ch === ">") depth--;
      if (ch === "," && depth === 0) {
        parts.push(current);
        current = "";
        continue;
      }
    }
    current += ch;
  }
  if (current.trim() !== "") parts.push(current);

  for (const part of parts) {
    const hint = parseLinkValue(part.trim(), baseUrl, now);
    if (hint !== null) hints.push(hint);
  }
  return hints;
}

function parseLinkValue(
  value: string,
  baseUrl: string,
  now: number,
): PreloadHint | null {
  const match = /^<([^>]*)>\s*(;.*)?$/.exec(value);
  if (match === null) return null;
  const target = match[1];
  const params = match[2] ?? "";

  const attrs = new Map<string, string | true>();
  for (const seg of params.split(";")) {
    const trimmed = seg.trim();
    if (!trimmed) continue;
    const eq = trimmed.indexOf("=");
    if (eq === -1) {
      attrs.set(trimmed.toLowerCase(), true);
    } else {
      const key = trimmed.slice(0, eq).trim().toLowerCase();
      let val = trimmed.slice(eq + 1).trim();
      if (val.startsWith('"') && val.endsWith('"') && val.length >= 2) {
        val = val.slice(1, -1);
      }
      attrs.set(key, val);
    }
  }

  const rel = attrs.get("rel");
  if (rel !== true && String(rel).toLowerCase() !== "preload") return null;

  const asAttr = attrs.get("as");
  if (asAttr === undefined || asAttr === true) return null;
  const as = asAttr.toLowerCase();
  if (as !== "script" && as !== "style" && as !== "image" && as !== "font") {
    return null;
  }

  let crossOrigin: CrossOriginMode | null = null;
  const coAttr = attrs.get("crossorigin");
  if (coAttr === true) {
    crossOrigin = "anonymous";
  } else if (typeof coAttr === "string") {
    const v = coAttr.toLowerCase();
    crossOrigin = v === "use-credentials" ? "use-credentials" : "anonymous";
  }

  const integrityAttr = attrs.get("integrity");
  const integrity =
    typeof integrityAttr === "string" && integrityAttr !== ""
      ? integrityAttr
      : null;

  let url: string;
  try {
    url = new URL(target, baseUrl).href;
  } catch {
    return null;
  }

  return { url, as, crossOrigin, integrity, recordedAt: now };
}

export interface PreloadHintStoreOptions {
  now?: () => number;
  ttlMs?: number;
}

/**
 * Tracks preload hints announced via 103 Early Hints and decides, once
 * the final response is known, whether an early-fetched resource may be
 * reused for a matching page tag.
 */
export class PreloadHintStore {
  private hints: PreloadHint[] = [];
  private policies: CspPolicy[] | null = null;
  private readonly now: () => number;
  private readonly ttlMs: number;

  constructor(options: PreloadHintStoreOptions = {}) {
    this.now = options.now ?? (() => Date.now());
    this.ttlMs = options.ttlMs ?? PRELOAD_HINT_TTL_MS;
  }

  /** Record hints from a 103 response's Link header(s). */
  addEarlyHints(linkHeaders: string | string[], baseUrl: string): void {
    const headers = Array.isArray(linkHeaders) ? linkHeaders : [linkHeaders];
    for (const header of headers) {
      for (const hint of parseEarlyHintLinks(header, baseUrl, this.now())) {
        this.hints.push(hint);
      }
    }
  }

  /**
   * Process the final response. Redirects (3xx) and non-2xx statuses
   * invalidate every recorded hint; 2xx responses attach the response's
   * CSP, which gates all subsequent reuse decisions.
   */
  onFinalResponse(response: FinalResponseInfo): void {
    const { status } = response;
    if (status < 200 || status >= 300) {
      this.clear();
      return;
    }
    this.policies = parseCspHeaders(response.cspHeaders ?? []);
    this.dropExpired();
  }

  /** Discard all recorded hints and any attached policy. */
  clear(): void {
    this.hints = [];
    this.policies = null;
  }

  /** Number of live (unexpired) hints. */
  get size(): number {
    this.dropExpired();
    return this.hints.length;
  }

  /**
   * Decide whether the resource early-fetched for `tag` may be reused.
   * A matching hint is consumed on success and discarded on any
   * mismatch, forcing a fresh fetch.
   */
  decide(tag: TagRequest, selfOrigin: string): ReuseDecision {
    this.dropExpired();
    const index = this.hints.findIndex(
      (h) => h.url === tag.url && h.as === tag.as,
    );
    if (index === -1) {
      return { reuse: false, reason: "no-hint" };
    }
    const hint = this.hints[index];

    const reject = (
      reason: Extract<
        ReuseDecision,
        { reuse: false }
      >["reason"],
    ): ReuseDecision => {
      this.hints.splice(index, 1);
      return { reuse: false, reason };
    };

    if (this.policies !== null) {
      const directive = directiveForAs(hint.as);
      if (!cspAllows(this.policies, directive, hint.url, selfOrigin)) {
        return reject("csp-blocked");
      }
    }

    if (hint.crossOrigin !== tag.crossOrigin) {
      return reject("crossorigin-mismatch");
    }

    if (hint.integrity !== tag.integrity) {
      return reject("integrity-mismatch");
    }

    this.hints.splice(index, 1);
    return { reuse: true, hint };
  }

  private dropExpired(): void {
    const cutoff = this.now() - this.ttlMs;
    this.hints = this.hints.filter((h) => h.recordedAt > cutoff);
  }
}
