import type { ContentScriptDeclaration, ManifestV3 } from "./types.ts";

export const MATCH_PATTERN_SCHEMES = [
  "http",
  "https",
  "file",
  "ftp",
  "ws",
  "wss",
  "urn",
] as const;

export const ALL_URLS_SCHEMES = new Set<string>(MATCH_PATTERN_SCHEMES);
export const SCHEME_WILDCARD_SCHEMES = new Set(["http", "https"]);

export class InvalidMatchPatternError extends Error {
  readonly pattern: string;
  readonly reason: string;

  constructor(pattern: string, reason: string) {
    super(`Invalid Chrome match pattern ${JSON.stringify(pattern)}: ${reason}`);
    this.name = "InvalidMatchPatternError";
    this.pattern = pattern;
    this.reason = reason;
  }
}

export interface ParsedMatchPattern {
  raw: string;
  allUrls: boolean;
  scheme: string | "*" | "<all_urls>";
  host: string;
  path: string;
  effectiveSchemes: readonly string[];
  hostKind: "any" | "suffix" | "exact" | "empty";
  hostSuffix?: string;
}

const SCHEME_RE = /^[a-z][a-z0-9+.-]*$/;
const HOST_RE = /^[a-z0-9._~%!$&'()*+,;=-]+$/i;

function stripTrailingDot(host: string): string {
  return host.endsWith(".") && host.length > 1 ? host.slice(0, -1) : host;
}

function normalizePath(path: string): string {
  return path.length === 0 ? "/" : path;
}

export function parseMatchPattern(input: string): ParsedMatchPattern {
  if (typeof input !== "string" || input.length === 0) {
    throw new InvalidMatchPatternError(String(input), "pattern must be a non-empty string");
  }

  const raw = input.trim();
  if (raw === "<all_urls>") {
    return {
      raw,
      allUrls: true,
      scheme: "<all_urls>",
      host: "*",
      path: "/*",
      effectiveSchemes: [...MATCH_PATTERN_SCHEMES],
      hostKind: "any",
    };
  }

  const schemeSeparator = raw.indexOf("://");
  if (schemeSeparator <= 0) {
    throw new InvalidMatchPatternError(input, "expected '<scheme>://<host><path>' or <all_urls>");
  }

  const scheme = raw.slice(0, schemeSeparator).toLowerCase();
  if (scheme !== "*" && !SCHEME_RE.test(scheme)) {
    throw new InvalidMatchPatternError(input, "scheme is not valid");
  }
  if (scheme !== "*" && !ALL_URLS_SCHEMES.has(scheme)) {
    throw new InvalidMatchPatternError(
      input,
      `scheme must be one of ${[...MATCH_PATTERN_SCHEMES].join(", ")}, *, or <all_urls>`,
    );
  }

  const remainder = raw.slice(schemeSeparator + 3);
  const slashIndex = remainder.indexOf("/");
  if (slashIndex < 0) {
    throw new InvalidMatchPatternError(input, "path component must start with '/'");
  }

  const host = remainder.slice(0, slashIndex).toLowerCase();
  const path = normalizePath(remainder.slice(slashIndex));
  if (!path.startsWith("/")) {
    throw new InvalidMatchPatternError(input, "path component must start with '/'");
  }

  if (scheme === "file") {
    if (host !== "") {
      throw new InvalidMatchPatternError(input, "file patterns must not contain a host");
    }
  } else if (host.length === 0) {
    throw new InvalidMatchPatternError(input, "host component must not be empty");
  }

  let hostKind: ParsedMatchPattern["hostKind"];
  let hostSuffix: string | undefined;
  if (scheme === "file") {
    hostKind = "empty";
  } else if (host === "*") {
    hostKind = "any";
  } else if (host.startsWith("*.")) {
    hostSuffix = stripTrailingDot(host.slice(2));
    if (hostSuffix.length === 0) {
      throw new InvalidMatchPatternError(input, "wildcard host requires a domain suffix");
    }
    if (hostSuffix.includes("*")) {
      throw new InvalidMatchPatternError(input, "host wildcard is only allowed as the leading '*.'");
    }
    validateHost(input, hostSuffix, true);
    hostKind = "suffix";
  } else {
    if (host.includes("*")) {
      throw new InvalidMatchPatternError(input, "host wildcard is only allowed as '*' or a leading '*.'");
    }
    validateHost(input, host, true);
    hostKind = "exact";
  }

  const effectiveSchemes =
    scheme === "*" ? [...SCHEME_WILDCARD_SCHEMES] : scheme === "file" ? ["file"] : [scheme];

  return {
    raw,
    allUrls: false,
    scheme,
    host,
    path,
    effectiveSchemes,
    hostKind,
    hostSuffix,
  };
}

function validateHost(input: string, host: string, allowEmpty: boolean): void {
  if (!allowEmpty && host.length === 0) {
    throw new InvalidMatchPatternError(input, "host component must not be empty");
  }
  if (host.startsWith("[") && host.endsWith("]")) {
    return;
  }
  if (host.includes(":")) {
    throw new InvalidMatchPatternError(input, "ports are not supported in Chrome match patterns");
  }
  if (!HOST_RE.test(host)) {
    throw new InvalidMatchPatternError(input, "host contains unsupported characters");
  }
}

export function isValidMatchPattern(input: string): boolean {
  try {
    parseMatchPattern(input);
    return true;
  } catch {
    return false;
  }
}

export function compileMatchPattern(input: string): {
  pattern: ParsedMatchPattern;
  matches: (url: string | URL) => boolean;
} {
  const pattern = parseMatchPattern(input);
  const pathRegExp = globToRegExp(pattern.path);

  const matches = (url: string | URL): boolean => {
    let parsed: URL;
    try {
      parsed = typeof url === "string" ? new URL(url) : url;
    } catch {
      return false;
    }

    const actualScheme = parsed.protocol.slice(0, -1).toLowerCase();
    if (!pattern.effectiveSchemes.includes(actualScheme)) {
      return false;
    }

    const actualHost = stripTrailingDot(parsed.hostname.toLowerCase());
    if (pattern.allUrls) {
      if (actualHost.length === 0 && actualScheme !== "urn" && actualScheme !== "file") {
        return false;
      }
      return true;
    } else if (pattern.hostKind === "empty") {
      if (actualHost !== "") {
        return false;
      }
    } else if (pattern.hostKind === "any") {
      if (actualHost.length === 0) {
        return false;
      }
    } else if (pattern.hostKind === "suffix") {
      const suffix = pattern.hostSuffix as string;
      if (actualHost !== suffix && !actualHost.endsWith(`.${suffix}`)) {
        return false;
      }
    } else if (actualHost !== pattern.host) {
      return false;
    }

    return pathRegExp.test(parsed.pathname);
  };

  return { pattern, matches };
}

const compiledCache = new Map<string, ReturnType<typeof compileMatchPattern>>();

export function matchPattern(pattern: string, url: string | URL): boolean {
  let compiled = compiledCache.get(pattern);
  if (!compiled) {
    compiled = compileMatchPattern(pattern);
    if (compiledCache.size > 512) {
      compiledCache.clear();
    }
    compiledCache.set(pattern, compiled);
  }
  return compiled.matches(url);
}

export function matchAnyPattern(patterns: readonly string[], url: string | URL): boolean {
  return patterns.some((pattern) => matchPattern(pattern, url));
}

export function matchesPatternSet(
  url: string | URL,
  includes: readonly string[],
  excludes: readonly string[] = [],
): boolean {
  return matchAnyPattern(includes, url) && !matchAnyPattern(excludes, url);
}

export function globToRegExp(glob: string): RegExp {
  let source = "^";
  for (const char of glob) {
    if (char === "*") {
      source += ".*";
    } else {
      source += char.replace(/[\\^$.*+?()[\]{}|]/g, "\\$&");
    }
  }
  source += "$";
  return new RegExp(source);
}

export function resourceGlobMatches(glob: string, resourcePath: string): boolean {
  const normalized = resourcePath.replace(/^\/+/, "");
  return globToRegExp(glob).test(normalized);
}

export function contentScriptsMatching(
  manifest: ManifestV3,
  url: string | URL,
): ContentScriptMatch[] {
  const parsedUrl = typeof url === "string" ? safeUrl(url) : url;
  if (!parsedUrl) return [];
  const urlString = parsedUrl.href;

  const matches: ContentScriptMatch[] = [];
  for (const [index, script] of (manifest.content_scripts ?? []).entries()) {
    const includes = toStringArray(script.matches);
    const excludes = toStringArray(script.exclude_matches ?? []);
    if (!matchesPatternSet(urlString, includes, excludes)) continue;

    const includeGlobs = toStringArray(script.include_globs ?? []);
    const excludeGlobs = toStringArray(script.exclude_globs ?? []);
    if (includeGlobs.length > 0 && !includeGlobs.some((glob) => globToRegExp(glob).test(urlString))) {
      continue;
    }
    if (excludeGlobs.some((glob) => globToRegExp(glob).test(urlString))) {
      continue;
    }

    matches.push({
      index,
      declaration: script,
      js: toStringArray(script.js ?? []),
      css: toStringArray(script.css ?? []),
    });
  }

  return matches;
}

export interface ContentScriptMatch {
  index: number;
  declaration: ContentScriptDeclaration;
  js: string[];
  css: string[];
}

function toStringArray(value: string | string[] | undefined): string[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? value : [value];
}

export function safeUrl(input: string, base?: string): URL | undefined {
  try {
    return base === undefined ? new URL(input) : new URL(input, base);
  } catch {
    return undefined;
  }
}

export function originOf(url: string | URL): string | undefined {
  const parsed = typeof url === "string" ? safeUrl(url) : url;
  if (!parsed) return undefined;
  if (parsed.origin !== "null") return parsed.origin;
  if (parsed.protocol === "file:") return "file://";
  return undefined;
}

export function isWebOrigin(origin: string): boolean {
  const parsed = safeUrl(origin);
  return Boolean(
    parsed &&
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.origin === origin.replace(/\/$/, ""),
  );
}

export function registrableDomainApprox(hostname: string): string {
  const labels = hostname.toLowerCase().split(".").filter(Boolean);
  if (labels.length <= 2) return labels.join(".");
  return labels.slice(-2).join(".");
}
