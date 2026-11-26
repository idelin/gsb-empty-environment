import { matchPattern, parseMatchPattern, safeUrl } from "./match-pattern.ts";
import { describePrivilegedTarget } from "./targets.ts";
import type { ManifestV3, PrivilegedOperation } from "./types.ts";

function toStringArray(value: string | string[] | undefined): string[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? [...value] : [value];
}

export function manifestAllowsOperation(
  manifest: ManifestV3,
  operation: PrivilegedOperation,
): boolean {
  const permissions = new Set(toStringArray(manifest.permissions));
  const required = operation.requiredPermissions ?? [];
  if (!required.every((permission) => permissions.has(permission))) return false;

  switch (operation.kind) {
    case "cookie-read":
      return permissions.has("cookies") && manifestCoversOperationTarget(manifest, operation);
    case "script-injection":
      return permissions.has("scripting") && manifestCoversOperationTarget(manifest, operation);
    case "dnr-bypass":
      return permissions.has("declarativeNetRequest") || permissions.has("declarativeNetRequestWithHostAccess");
    case "fetch-proxy":
      return manifestCoversOperationTarget(manifest, operation);
    case "storage-read":
      return permissions.has("storage");
    case "custom":
      return true;
    default:
      return false;
  }
}

export function manifestCoversOperationTarget(
  manifest: ManifestV3,
  operation: PrivilegedOperation,
): boolean {
  const patterns = toStringArray(manifest.host_permissions);
  if (patterns.length === 0) return false;
  const descriptor = describePrivilegedTarget(operation);
  if (descriptor.kind === "global") {
    return patterns.some((pattern) => pattern === "<all_urls>" || pattern === "*://*/*");
  }

  if (descriptor.kind === "url" && descriptor.origin) {
    return patterns.some((pattern) => safelyMatches(pattern, `${descriptor.origin}/`));
  }

  const host = descriptor.host ?? "example.invalid";
  const schemes =
    descriptor.scheme === "*" || descriptor.scheme === "<all_urls>"
      ? ["http", "https"]
      : [descriptor.scheme ?? "https"];

  if (descriptor.kind === "pattern" && descriptor.patternHostKind === "any") {
    return patterns.some((pattern) => {
      try {
        const parsed = parseMatchPattern(pattern);
        return parsed.hostKind === "any" || parsed.allUrls;
      } catch {
        return false;
      }
    });
  }

  const candidates = new Set<string>();
  for (const scheme of schemes) {
    if (descriptor.kind === "pattern" && descriptor.patternHostKind === "suffix") {
      candidates.add(`${scheme}://${host}/`);
      candidates.add(`${scheme}://sub.${host}/`);
    } else {
      candidates.add(`${scheme}://${host}/`);
    }
  }
  return [...candidates].every((candidate) =>
    patterns.some((pattern) => safelyMatches(pattern, candidate)),
  );
}

function safelyMatches(pattern: string, url: string): boolean {
  try {
    return matchPattern(pattern, url);
  } catch {
    return false;
  }
}

export function targetSampleUrl(operation: PrivilegedOperation): string | undefined {
  const descriptor = describePrivilegedTarget(operation);
  if (descriptor.kind === "url") return operation.target;
  if (descriptor.host) {
    const scheme = descriptor.scheme && descriptor.scheme !== "*" ? descriptor.scheme : "https";
    return `${scheme}://${descriptor.host}/`;
  }
  return undefined;
}

export function normalizeTargetUrl(operation: PrivilegedOperation): URL | undefined {
  const sample = targetSampleUrl(operation);
  return sample ? safeUrl(sample) : undefined;
}
