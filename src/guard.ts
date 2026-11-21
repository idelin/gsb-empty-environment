import { TypeGuardError, fail } from "./errors.ts";
import {
  canonicalMime,
  decideDisposition,
  extensionsFor,
  mimeForExtension,
  sniffMatches,
  sniffType,
} from "./mime.ts";

/** A single server-side representation of a resource. */
export interface Representation {
  /** Authoritative declared MIME type (the server's Content-Type). */
  declaredType: string;
  /** Filename whose extension is cross-checked against the declared type. */
  filename?: string;
  /** Response body used for magic-byte sniffing. */
  body?: Uint8Array;
  /** Upstream/existing response headers (names are case-insensitive). */
  headers?: Record<string, string>;
}

/** The request side of the exchange. Neither field may drive type/disposition. */
export interface RequestContext {
  query?: Record<string, string | string[]>;
  headers?: Record<string, string>;
}

export interface GuardedHeaders {
  "content-type": string;
  "x-content-type-options": "nosniff";
  "content-disposition": string;
  vary?: string;
}

export interface MultipartPartOutput {
  name: string;
  declaredType: string;
  headers: GuardedHeaders;
  body?: Uint8Array;
}

export interface MultipartOutput {
  boundary: string;
  headers: {
    "content-type": string;
    "x-content-type-options": "nosniff";
  };
  parts: MultipartPartOutput[];
}

const TYPE_QUERY_KEYS = new Set([
  "type",
  "mime",
  "mimetype",
  "content-type",
  "contenttype",
  "format",
  "as",
  "response-content-type",
]);

const DISPOSITION_QUERY_KEYS = new Set([
  "disposition",
  "inline",
  "attachment",
  "download",
  "response-content-disposition",
]);

const TYPE_REQUEST_HEADERS = new Set([
  "content-type",
  "x-response-content-type",
  "x-requested-content-type",
]);

const DISPOSITION_REQUEST_HEADERS = new Set([
  "content-disposition",
  "x-response-content-disposition",
  "x-requested-disposition",
]);

function headerGet(headers: Record<string, string>, name: string): string | undefined {
  const needle = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === needle) return value;
  }
  return undefined;
}

function asArray(value: string | string[]): string[] {
  return Array.isArray(value) ? value : [value];
}

function dispositionToken(raw: string): "inline" | "attachment" | undefined {
  const token = raw.split(";", 1)[0]!.trim().toLowerCase();
  return token === "inline" || token === "attachment" ? token : undefined;
}

function queryDispositionHint(key: string, raw: string): "inline" | "attachment" | undefined {
  if ((key === "inline" || key === "attachment" || key === "download") &&
    (raw === "false" || raw === "0")) {
    return undefined;
  }
  if (key === "inline") return "inline";
  if (key === "attachment" || key === "download") return "attachment";
  return dispositionToken(raw);
}

function extensionOf(filename: string): string | undefined {
  const slash = Math.max(filename.lastIndexOf("/"), filename.lastIndexOf("\\"));
  const base = slash >= 0 ? filename.slice(slash + 1) : filename;
  const dot = base.lastIndexOf(".");
  if (dot <= 0 || dot === base.length - 1) return undefined;
  return base.slice(dot + 1).toLowerCase();
}

function sanitizeFilename(filename: string): string {
  const slash = Math.max(filename.lastIndexOf("/"), filename.lastIndexOf("\\"));
  const base = slash >= 0 ? filename.slice(slash + 1) : filename;
  return base.replace(/[\x00-\x1f\x7f"\\]/g, "_").trim() || "download";
}

function rfc5987Encode(value: string): string {
  return encodeURIComponent(value).replace(/['()*]/g, (ch) =>
    "%" + ch.charCodeAt(0).toString(16).toUpperCase(),
  );
}

function buildContentDisposition(
  disposition: "inline" | "attachment",
  filename: string | undefined,
): string {
  if (!filename) return disposition;
  const safe = sanitizeFilename(filename);
  const ascii = safe.replace(/[^\x20-\x7e]/g, "_").replace(/"/g, "_");
  const encoded = rfc5987Encode(safe);
  return `${disposition}; filename="${ascii}"; filename*=UTF-8''${encoded}`;
}

/**
 * Reject request-side attempts to override the declared type or the computed
 * disposition. The `Accept` header is deliberately not consulted: it may
 * select a representation but can never change the declared MIME type.
 */
function checkRequestOverrides(
  request: RequestContext | undefined,
  declaredMime: string,
  requiredDisposition: "inline" | "attachment",
): void {
  if (!request) return;

  if (request.query) {
    for (const [rawKey, rawValue] of Object.entries(request.query)) {
      const key = rawKey.toLowerCase();
      for (const value of asArray(rawValue)) {
        if (TYPE_QUERY_KEYS.has(key) && value.trim() !== "") {
          const requested = canonicalMime(value);
          if (requested !== declaredMime) {
            fail("negotiation-bypass", `query parameter "${rawKey}" attempts to override declared type`, {
              parameter: rawKey,
              requested,
              declared: declaredMime,
            });
          }
        }
        if (DISPOSITION_QUERY_KEYS.has(key)) {
          const requested = queryDispositionHint(key, value);
          if (requested && requested !== requiredDisposition) {
            fail(
              "negotiation-bypass",
              `query parameter "${rawKey}" attempts to decide content disposition`,
              { parameter: rawKey, requested, required: requiredDisposition },
            );
          }
        }
      }
    }
  }

  if (request.headers) {
    for (const [rawKey, value] of Object.entries(request.headers)) {
      const key = rawKey.toLowerCase();
      if (TYPE_REQUEST_HEADERS.has(key)) {
        const requested = canonicalMime(value);
        if (requested !== declaredMime) {
          fail("negotiation-bypass", `request header "${rawKey}" attempts to override declared type`, {
            header: rawKey,
            requested,
            declared: declaredMime,
          });
        }
      }
      if (DISPOSITION_REQUEST_HEADERS.has(key)) {
        const requested = dispositionToken(value);
        if (requested && requested !== requiredDisposition) {
          fail(
            "negotiation-bypass",
            `request header "${rawKey}" attempts to decide content disposition`,
            { header: rawKey, requested, required: requiredDisposition },
          );
        }
      }
    }
  }
}

/** Mismatch class 1: filename extension must agree with the declared type. */
function checkExtension(declaredMime: string, filename: string | undefined): void {
  if (!filename) {
    fail("extension-mismatch", "download requires a filename for extension cross-check", {
      declared: declaredMime,
    });
  }
  const ext = extensionOf(filename!);
  if (!ext) {
    fail("extension-mismatch", `filename "${filename}" has no extension`, {
      declared: declaredMime,
      filename,
    });
  }
  const allowed = extensionsFor(declaredMime);
  if (!allowed || !allowed.includes(ext!)) {
    fail("extension-mismatch", `extension ".${ext}" is not valid for declared type ${declaredMime}`, {
      declared: declaredMime,
      extension: ext,
      filename,
      allowed: allowed ?? [],
    });
  }
  const implied = mimeForExtension(ext!);
  if (implied && implied !== declaredMime) {
    fail("extension-mismatch", `extension ".${ext}" implies ${implied}, not ${declaredMime}`, {
      declared: declaredMime,
      implied,
      extension: ext,
      filename,
    });
  }
}

/** Mismatch class 2: magic bytes must agree with the declared type. */
function checkSniff(declaredMime: string, body: Uint8Array | undefined): void {
  if (!body) return;
  const sniffed = sniffType(body);
  if (!sniffMatches(declaredMime, sniffed)) {
    fail("content-sniff-mismatch", `sniffed content type ${String(sniffed)} conflicts with ${declaredMime}`, {
      declared: declaredMime,
      sniffed: sniffed ?? "unknown",
    });
  }
}

/** Mismatch class 3: upstream disposition must match the type-derived one. */
function checkUpstreamDisposition(
  existing: Record<string, string> | undefined,
  required: "inline" | "attachment",
): void {
  if (!existing) return;
  const raw = headerGet(existing, "content-disposition");
  if (!raw) return;
  const actual = dispositionToken(raw);
  if (actual && actual !== required) {
    fail("disposition-mismatch", `upstream disposition "${actual}" conflicts with required "${required}"`, {
      actual,
      required,
    });
  }
}

function checkUpstreamContentType(
  existing: Record<string, string> | undefined,
  declaredMime: string,
): void {
  if (!existing) return;
  const raw = headerGet(existing, "content-type");
  if (raw && canonicalMime(raw) !== declaredMime) {
    fail("negotiation-bypass", "upstream content-type header conflicts with declared type", {
      declared: declaredMime,
      upstream: canonicalMime(raw),
    });
  }
}

/**
 * Core guard for one representation. Produces the hardened response headers:
 * `X-Content-Type-Options: nosniff` is always attached and disposition is
 * derived from the declared type alone.
 */
export function guardRepresentation(
  representation: Representation,
  request?: RequestContext,
): GuardedHeaders {
  const declaredMime = canonicalMime(representation.declaredType);
  if (!declaredMime || declaredMime === "*/*" || declaredMime.includes(" ")) {
    fail("extension-mismatch", `invalid declared type "${representation.declaredType}"`, {
      declared: representation.declaredType,
    });
  }

  const decision = decideDisposition(declaredMime);

  checkRequestOverrides(request, declaredMime, decision.disposition);
  checkUpstreamContentType(representation.headers, declaredMime);
  checkExtension(declaredMime, representation.filename);
  checkSniff(declaredMime, representation.body);
  checkUpstreamDisposition(representation.headers, decision.disposition);

  return {
    "content-type": declaredMime,
    "x-content-type-options": "nosniff",
    "content-disposition": buildContentDisposition(decision.disposition, representation.filename),
  };
}

/** Guard a single download response. */
export function guardResponse(
  representation: Representation,
  request?: RequestContext,
): GuardedHeaders {
  return guardRepresentation(representation, request);
}

/**
 * Content negotiation: every representation of the same resource must declare
 * the same MIME type. A differing representation set is a negotiation bypass.
 */
export function guardNegotiated(
  representations: Representation[],
  request?: RequestContext,
): GuardedHeaders {
  if (representations.length === 0) {
    fail("negotiation-bypass", "content negotiation requires at least one representation", {});
  }
  const declaredTypes = new Set(representations.map((rep) => canonicalMime(rep.declaredType)));
  if (declaredTypes.size > 1) {
    fail("negotiation-bypass", "representations of the same resource declare inconsistent types", {
      declaredTypes: [...declaredTypes],
    });
  }
  const headers = representations.map((rep) => guardRepresentation(rep, request));
  return { ...headers[0]!, vary: "Accept" };
}

function generateBoundary(): string {
  const bytes = new Uint8Array(24);
  globalThis.crypto.getRandomValues(bytes);
  return "----typeguard" + Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * Multipart response: each part is guarded independently. When
 * `sameResource` is set, all parts must declare the same MIME type, matching
 * the content-negotiation rule.
 */
export function guardMultipart(
  parts: Array<Representation & { name: string }>,
  request?: RequestContext,
  options: { sameResource?: boolean } = {},
): MultipartOutput {
  if (parts.length === 0) {
    fail("negotiation-bypass", "multipart response requires at least one part", {});
  }
  if (options.sameResource) {
    const declaredTypes = new Set(parts.map((part) => canonicalMime(part.declaredType)));
    if (declaredTypes.size > 1) {
      fail("negotiation-bypass", "multipart parts of the same resource declare inconsistent types", {
        declaredTypes: [...declaredTypes],
      });
    }
  }

  const outputParts: MultipartPartOutput[] = parts.map((part) => {
    const { name, ...representation } = part;
    return {
      name,
      declaredType: canonicalMime(representation.declaredType),
      headers: guardRepresentation(representation, request),
      body: representation.body,
    };
  });

  const boundary = generateBoundary();
  return {
    boundary,
    headers: {
      "content-type": `multipart/mixed; boundary=${boundary}`,
      "x-content-type-options": "nosniff",
    },
    parts: outputParts,
  };
}

export { TypeGuardError };
