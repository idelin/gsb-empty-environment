import {
  matchAnyPattern,
  matchPattern,
  parseMatchPattern,
  safeUrl,
} from "./match-pattern.ts";
import type { PrivilegedOperation, PrivilegedOperationKind } from "./types.ts";

export interface TargetDescriptor {
  raw?: string;
  kind: "url" | "domain" | "pattern" | "global";
  origin?: string;
  host?: string;
  pattern?: string;
  patternHostKind?: "any" | "suffix" | "exact" | "empty";
  scheme?: string;
  wildcard: boolean;
}

export function describePrivilegedTarget(operation: PrivilegedOperation): TargetDescriptor {
  const target = operation.target;
  if (typeof target !== "string" || target.length === 0) {
    return { kind: "global", wildcard: true };
  }

  const targetKind = operation.targetKind ?? "url";
  if (targetKind === "domain") {
    const host = target.replace(/^\*\./, "").replace(/^\./, "").toLowerCase();
    return {
      raw: target,
      kind: "domain",
      host,
      pattern: `*://*.${host}/*`,
      wildcard: true,
    };
  }

  if (targetKind === "pattern") {
    try {
      const parsed = parseMatchPattern(target);
      return {
        raw: target,
        kind: "pattern",
        pattern: parsed.raw,
        host: parsed.hostSuffix ?? parsed.host,
        patternHostKind: parsed.hostKind,
        scheme: parsed.scheme,
        wildcard: parsed.hostKind === "any" || parsed.scheme === "*" || parsed.allUrls,
      };
    } catch {
      return { raw: target, kind: "pattern", pattern: target, wildcard: true };
    }
  }

  const parsed = safeUrl(target);
  if (!parsed || parsed.origin === "null") {
    return { raw: target, kind: "url", wildcard: true };
  }
  return {
    raw: target,
    kind: "url",
    origin: parsed.origin,
    host: parsed.hostname.toLowerCase(),
    wildcard: false,
  };
}

export function normalizeOrigin(origin: string): string | undefined {
  const parsed = safeUrl(origin);
  if (!parsed) return undefined;
  if (parsed.origin !== "null") return parsed.origin;
  if (parsed.protocol === "file:") return "file://";
  return undefined;
}

export function senderMatchesTabPermissions(
  senderOrigin: string,
  tabPermissions: readonly string[],
): boolean {
  if (tabPermissions.length === 0) return false;
  if (tabPermissions.includes("activeTab")) return true;
  const senderUrl = `${senderOrigin.replace(/\/$/, "")}/`;
  return tabPermissions.some((permission) => {
    if (permission === "activeTab") return true;
    try {
      return matchPattern(permission, senderUrl);
    } catch {
      return false;
    }
  });
}

export function targetMatchesPermissionPatterns(
  operation: PrivilegedOperation,
  patterns: readonly string[],
): boolean {
  if (patterns.length === 0) return false;
  if (patterns.includes("activeTab")) {
    return operation.target ? matchTargetAgainstCurrentTab(operation, patterns) : true;
  }

  const descriptor = describePrivilegedTarget(operation);
  if (descriptor.kind === "global") {
    return patterns.some((pattern) => pattern === "<all_urls>" || pattern === "*://*/*");
  }
  if (descriptor.kind === "url" && descriptor.origin) {
    const candidate = `${descriptor.origin.replace(/\/$/, "")}/`;
    return patterns.some((pattern) => {
      try {
        return matchPattern(pattern, candidate);
      } catch {
        return false;
      }
    });
  }
  if (descriptor.pattern) {
    const candidateHost = descriptor.host ?? "example.invalid";
    const protocols = descriptor.scheme === "*" ? ["http", "https"] : descriptor.scheme
      ? [descriptor.scheme]
      : ["http", "https"];
    for (const protocol of protocols) {
      const candidate = `${protocol}://${candidateHost}/`;
      if (patterns.some((pattern) => safelyMatches(pattern, candidate))) return true;
    }
    return false;
  }
  return false;
}

function matchTargetAgainstCurrentTab(
  operation: PrivilegedOperation,
  patterns: readonly string[],
): boolean {
  const descriptor = describePrivilegedTarget(operation);
  if (descriptor.kind === "global") return true;
  const host = descriptor.host ?? "example.invalid";
  const protocol = descriptor.scheme && descriptor.scheme !== "*" ? descriptor.scheme : "https";
  return patterns.some((pattern) => safelyMatches(pattern, `${protocol}://${host}/`));
}

function safelyMatches(pattern: string, url: string): boolean {
  try {
    return matchPattern(pattern, url);
  } catch {
    return false;
  }
}

export function targetWithinSenderOriginBoundary(
  operation: PrivilegedOperation,
  senderOrigin: string,
): boolean {
  const target = describePrivilegedTarget(operation);
  if (target.kind === "global") return false;
  const sender = safeUrl(senderOrigin);
  if (!sender || sender.origin === "null") return false;
  const senderHost = sender.hostname.toLowerCase();

  if (target.kind === "url") {
    return target.origin === sender.origin;
  }

  if (target.kind === "domain") {
    const host = target.host as string;
    if (operation.kind === "cookie-read" || operation.kind === "storage-read") {
      return senderHost === host || senderHost.endsWith(`.${host}`);
    }
    return senderHost === host;
  }

  if (target.kind === "pattern") {
    const host = target.host;
    if (!host || target.patternHostKind === "any") return false;

    if (target.scheme === "*" || target.scheme === "<all_urls>") {
      return operation.kind === "cookie-read" || operation.kind === "storage-read";
    }
    if (target.scheme !== sender.protocol.slice(0, -1)) return false;

    if (target.patternHostKind === "exact") {
      return host === senderHost;
    }
    if (target.patternHostKind === "suffix") {
      if (operation.kind === "cookie-read" || operation.kind === "storage-read") {
        return senderHost === host || senderHost.endsWith(`.${host}`);
      }
      return false;
    }
    return false;
  }

  return false;
}

export function targetAllowedByToken(
  operation: PrivilegedOperation,
  tokenTarget: string | undefined,
): boolean {
  const target = describePrivilegedTarget(operation);
  if (target.kind === "global") return tokenTarget === undefined;
  if (!tokenTarget) return false;
  if (tokenTarget === target.raw) return true;

  const tokenDescriptor = describeTargetString(tokenTarget);
  try {
    if (
      tokenDescriptor.kind === "url" &&
      tokenDescriptor.origin &&
      target.kind === "url" &&
      tokenDescriptor.origin === target.origin
    ) {
      return true;
    }
    if (target.kind === "url" && target.origin) {
      return matchAnyPattern([tokenTarget], `${target.origin.replace(/\/$/, "")}/`);
    }
    if (target.kind === "domain" && target.host) {
      return matchAnyPattern([tokenTarget], `https://${target.host}/`);
    }
    if (target.kind === "pattern" && target.pattern && tokenTarget === target.pattern) {
      return true;
    }
  } catch {
    return false;
  }
  return false;
}

function describeTargetString(target: string): TargetDescriptor {
  if (isLikelyMatchPattern(target)) {
    return describePrivilegedTarget({ kind: "custom", target, targetKind: "pattern" });
  }
  const parsed = safeUrl(target);
  if (parsed) {
    return describePrivilegedTarget({ kind: "custom", target });
  }
  return describePrivilegedTarget({ kind: "custom", target, targetKind: "domain" });
}

function isLikelyMatchPattern(value: string): boolean {
  return value === "<all_urls>" || value.includes("://");
}

export function isSensitivePrivilegedOperation(kind: PrivilegedOperationKind): boolean {
  return (
    kind === "cookie-read" ||
    kind === "script-injection" ||
    kind === "dnr-bypass" ||
    kind === "fetch-proxy" ||
    kind === "storage-read" ||
    kind === "custom"
  );
}

export function operationTargetCrossesSenderOrigin(
  operation: PrivilegedOperation,
  senderOrigin: string,
): boolean {
  if (!isSensitivePrivilegedOperation(operation.kind)) return false;
  return !targetWithinSenderOriginBoundary(operation, senderOrigin);
}
