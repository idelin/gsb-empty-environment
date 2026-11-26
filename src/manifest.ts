import {
  contentScriptsMatching,
  isValidMatchPattern,
  matchAnyPattern,
  matchPattern,
  parseMatchPattern,
  resourceGlobMatches,
  safeUrl,
} from "./match-pattern.ts";
import type {
  ContentScriptMatch,
  DnrRuleResource,
  ExternallyConnectable,
  ManifestV3,
  Severity,
  WebAccessibleResourceEntry,
} from "./types.ts";

export class InvalidManifestError extends Error {
  readonly issues: ManifestIssue[];

  constructor(issues: ManifestIssue[]) {
    super(`Invalid MV3 manifest: ${issues.map((issue) => `${issue.path}: ${issue.message}`).join("; ")}`);
    this.name = "InvalidManifestError";
    this.issues = issues;
  }
}

export interface ManifestIssue {
  code: string;
  path: string;
  message: string;
  severity: Severity;
}

export interface NormalizedWarEntry {
  index: number;
  resources: string[];
  matches: string[];
  extensionIds: string[];
  useDynamicUrl: boolean;
}

export interface WarExposure {
  entryIndex: number;
  resource: string;
  origin?: string;
  extensionId?: string;
  matches: string[];
  extensionIds: string[];
  useDynamicUrl: boolean;
}

export interface WarClickjackingFinding {
  entryIndex: number;
  privilegedPage: string;
  exposedResourceGlob: string;
  webMatches: string[];
  exposedToAllUrls: boolean;
  severity: Severity;
}

export interface HostPermissionGrant {
  pattern: string;
  source: "host_permissions" | "optional_host_permissions" | "legacy_permission";
  optional: boolean;
  granted: boolean;
}

export interface PermissionSolverOptions {
  grantedOptionalPermissions?: string[];
  grantedOptionalHostPermissions?: string[];
}

export interface ApiPermissionStatus {
  permission: string;
  declared: boolean;
  optional: boolean;
  granted: boolean;
}

export interface ManifestAccessProfile {
  cookies: boolean;
  scripting: boolean;
  declarativeNetRequest: boolean;
  webRequest: boolean;
  tabs: boolean;
  powerfulApis: string[];
}

export interface PermissionSolution {
  manifest: ManifestV3;
  apiPermissions: string[];
  optionalApiPermissions: string[];
  hostPermissions: HostPermissionGrant[];
  hostPermissionPatterns: string[];
  contentScriptsForUrl: (url: string | URL) => ContentScriptMatch[];
  isHostPermitted: (url: string | URL) => boolean;
  isContentScriptInjected: (url: string | URL) => boolean;
  isExternallyConnectable: (originOrExtensionId: string) => boolean;
  externallyConnectable: ExternallyConnectable | undefined;
  webAccessibleResources: NormalizedWarEntry[];
  isWebAccessibleForOrigin: (resource: string, origin: string) => boolean;
  warExposuresForOrigin: (resource: string, origin: string) => WarExposure[];
  privilegedPages: string[];
  isPrivilegedPage: (resource: string) => boolean;
  warClickjackingFindings: WarClickjackingFinding[];
  dnrRuleResources: DnrRuleResource[];
  accessProfile: ManifestAccessProfile;
  apiPermissionStatus: (permission: string) => ApiPermissionStatus;
  hasApiPermission: (permission: string) => boolean;
  issues: ManifestIssue[];
}

const API_PERMISSIONS = new Set([
  "activeTab",
  "alarms",
  "background",
  "bookmarks",
  "clipboardRead",
  "clipboardWrite",
  "contentSettings",
  "contextMenus",
  "cookies",
  "debugger",
  "declarativeNetRequest",
  "declarativeNetRequestFeedback",
  "declarativeNetRequestWithHostAccess",
  "desktopCapture",
  "downloads",
  "enterprise.deviceAttributes",
  "enterprise.hardwarePlatform",
  "enterprise.networkingAttributes",
  "enterprise.platformKeys",
  "favicon",
  "fileBrowserHandler",
  "fileSystemProvider",
  "fontSettings",
  "gcm",
  "geolocation",
  "history",
  "identity",
  "idle",
  "management",
  "nativeMessaging",
  "notifications",
  "pageCapture",
  "platformKeys",
  "power",
  "printerProvider",
  "privacy",
  "proxy",
  "scripting",
  "search",
  "sessions",
  "sidePanel",
  "storage",
  "system.cpu",
  "system.display",
  "system.memory",
  "system.storage",
  "tabCapture",
  "tabGroups",
  "tabs",
  "topSites",
  "tts",
  "ttsEngine",
  "unlimitedStorage",
  "webAuthenticationProxy",
  "webNavigation",
  "webRequest",
  "webRequestBlocking",
]);

const POWERFUL_PERMISSIONS = new Set([
  "cookies",
  "scripting",
  "declarativeNetRequest",
  "declarativeNetRequestWithHostAccess",
  "debugger",
  "webRequest",
  "webRequestBlocking",
  "proxy",
  "privacy",
  "nativeMessaging",
  "management",
  "downloads",
  "history",
  "bookmarks",
  "clipboardRead",
  "tabCapture",
]);

function toStringArray(value: string | string[] | undefined): string[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? [...value] : [value];
}

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key);
}

export function validateManifest(input: unknown): ManifestIssue[] {
  const issues: ManifestIssue[] = [];
  if (input === null || typeof input !== "object" || Array.isArray(input)) {
    return [{ code: "not-object", path: "$", message: "manifest must be a JSON object", severity: "critical" }];
  }
  const manifest = input as Record<string, unknown>;
  if (manifest.manifest_version !== 3) {
    issues.push({
      code: "manifest-version",
      path: "manifest_version",
      message: "only Manifest V3 (manifest_version: 3) is supported",
      severity: "critical",
    });
    return issues;
  }

  validatePermissionList(manifest.permissions, "permissions", issues, false);
  validatePermissionList(manifest.optional_permissions, "optional_permissions", issues, true);
  validateHostPatternList(manifest.host_permissions, "host_permissions", issues);
  validateHostPatternList(manifest.optional_host_permissions, "optional_host_permissions", issues);

  if (manifest.content_scripts !== undefined) {
    if (!Array.isArray(manifest.content_scripts)) {
      issues.push({
        code: "content-scripts-type",
        path: "content_scripts",
        message: "content_scripts must be an array",
        severity: "high",
      });
    } else {
      manifest.content_scripts.forEach((script, index) => {
        if (script === null || typeof script !== "object") {
          issues.push({
            code: "content-script-type",
            path: `content_scripts[${index}]`,
            message: "content script declaration must be an object",
            severity: "high",
          });
          return;
        }
        const declaration = script as Record<string, unknown>;
        const matches = toStringArray(declaration.matches as string | string[] | undefined);
        if (matches.length === 0) {
          issues.push({
            code: "content-script-matches",
            path: `content_scripts[${index}].matches`,
            message: "content script requires at least one match pattern",
            severity: "high",
          });
        }
        validateMatchPatterns(matches, `content_scripts[${index}].matches`, issues);
        validateMatchPatterns(
          toStringArray(declaration.exclude_matches as string | string[] | undefined),
          `content_scripts[${index}].exclude_matches`,
          issues,
        );
      });
    }
  }

  if (manifest.web_accessible_resources !== undefined) {
    if (!Array.isArray(manifest.web_accessible_resources)) {
      issues.push({
        code: "war-type",
        path: "web_accessible_resources",
        message: "web_accessible_resources must be an array of objects in MV3",
        severity: "high",
      });
    } else {
      manifest.web_accessible_resources.forEach((entry, index) => {
        if (entry === null || typeof entry !== "object" || Array.isArray(entry)) {
          issues.push({
            code: "war-entry-type",
            path: `web_accessible_resources[${index}]`,
            message: "each web_accessible_resources entry must be an object",
            severity: "high",
          });
          return;
        }
        const war = entry as Record<string, unknown>;
        const resources = toStringArray(war.resources as string | string[] | undefined);
        if (resources.length === 0) {
          issues.push({
            code: "war-resources",
            path: `web_accessible_resources[${index}].resources`,
            message: "WAR entry requires a non-empty resources array",
            severity: "high",
          });
        }
        const matches = toStringArray(war.matches as string | string[] | undefined);
        const extensionIds = toStringArray(war.extension_ids as string | string[] | undefined);
        if (matches.length === 0 && extensionIds.length === 0) {
          issues.push({
            code: "war-scope",
            path: `web_accessible_resources[${index}]`,
            message: "WAR entry requires matches and/or extension_ids",
            severity: "high",
          });
        }
        validateMatchPatterns(matches, `web_accessible_resources[${index}].matches`, issues);
      });
    }
  }

  if (manifest.externally_connectable !== undefined) {
    if (manifest.externally_connectable === null || typeof manifest.externally_connectable !== "object") {
      issues.push({
        code: "externally-connectable-type",
        path: "externally_connectable",
        message: "externally_connectable must be an object",
        severity: "high",
      });
    } else {
      const external = manifest.externally_connectable as Record<string, unknown>;
      validateMatchPatterns(
        toStringArray(external.matches as string | string[] | undefined),
        "externally_connectable.matches",
        issues,
      );
    }
  }

  if (manifest.declarative_net_request !== undefined) {
    if (
      manifest.declarative_net_request === null ||
      typeof manifest.declarative_net_request !== "object"
    ) {
      issues.push({
        code: "dnr-type",
        path: "declarative_net_request",
        message: "declarative_net_request must be an object",
        severity: "high",
      });
    } else {
      const dnr = manifest.declarative_net_request as Record<string, unknown>;
      if (!Array.isArray(dnr.rule_resources)) {
        issues.push({
          code: "dnr-rules-type",
          path: "declarative_net_request.rule_resources",
          message: "rule_resources must be an array",
          severity: "high",
        });
      } else {
        const ids = new Set<string>();
        dnr.rule_resources.forEach((resourceValue, index) => {
          const resource = resourceValue as Record<string, unknown>;
          if (typeof resource !== "object" || resource === null) {
            issues.push({
              code: "dnr-resource-type",
              path: `declarative_net_request.rule_resources[${index}]`,
              message: "rule resource must be an object",
              severity: "high",
            });
            return;
          }
          if (typeof resource.id !== "string" || resource.id.length === 0) {
            issues.push({
              code: "dnr-resource-id",
              path: `declarative_net_request.rule_resources[${index}].id`,
              message: "rule resource id must be a non-empty string",
              severity: "high",
            });
          } else if (ids.has(resource.id)) {
            issues.push({
              code: "dnr-resource-duplicate",
              path: `declarative_net_request.rule_resources[${index}].id`,
              message: `duplicate DNR rule resource id ${resource.id}`,
              severity: "medium",
            });
          } else {
            ids.add(resource.id);
          }
          if (typeof resource.path !== "string" || !resource.path.endsWith(".json")) {
            issues.push({
              code: "dnr-resource-path",
              path: `declarative_net_request.rule_resources[${index}].path`,
              message: "DNR rule resource path must be a JSON file",
              severity: "medium",
            });
          }
          if (typeof resource.enabled !== "boolean") {
            issues.push({
              code: "dnr-resource-enabled",
              path: `declarative_net_request.rule_resources[${index}].enabled`,
              message: "rule resource enabled must be a boolean",
              severity: "medium",
            });
          }
        });
      }
    }
  }

  return issues;
}

function validatePermissionList(
  value: unknown,
  path: string,
  issues: ManifestIssue[],
  optional: boolean,
): void {
  if (value === undefined) return;
  const entries = toStringArray(value as string | string[] | undefined);
  if (typeof value !== "string" && !Array.isArray(value)) {
    issues.push({
      code: "permission-type",
      path,
      message: `${path} must be a string or array of strings`,
      severity: "high",
    });
    return;
  }
  for (const permission of entries) {
    if (typeof permission !== "string" || permission.length === 0) {
      issues.push({
        code: "permission-entry",
        path,
        message: "permission entries must be non-empty strings",
        severity: "high",
      });
      continue;
    }
    if (permission === "<all_urls>" || permission.includes("://")) {
      if (optional) {
        issues.push({
          code: "host-permission-in-optional-permissions",
          path,
          message:
            "host match patterns belong in optional_host_permissions; optional_permissions is for API permissions",
          severity: "medium",
        });
      } else {
        issues.push({
          code: "host-permission-in-permissions",
          path,
          message: "host match patterns belong in host_permissions in MV3",
          severity: "high",
        });
      }
      continue;
    }
    if (!API_PERMISSIONS.has(permission) && !permission.startsWith("http")) {
      issues.push({
        code: "unknown-permission",
        path,
        message: `unknown API permission ${permission}`,
        severity: "info",
      });
    }
  }
}

function validateHostPatternList(value: unknown, path: string, issues: ManifestIssue[]): void {
  if (value === undefined) return;
  if (typeof value !== "string" && !Array.isArray(value)) {
    issues.push({
      code: "host-pattern-type",
      path,
      message: `${path} must be a string or array of strings`,
      severity: "high",
    });
    return;
  }
  validateMatchPatterns(toStringArray(value as string | string[]), path, issues);
}

function validateMatchPatterns(patterns: readonly string[], path: string, issues: ManifestIssue[]): void {
  for (const [index, pattern] of patterns.entries()) {
    if (typeof pattern !== "string" || !isValidMatchPattern(pattern)) {
      issues.push({
        code: "match-pattern",
        path: `${path}[${index}]`,
        message: `invalid Chrome match pattern ${JSON.stringify(pattern)}`,
        severity: "high",
      });
    }
  }
}

export function parseManifest(
  input: string | unknown,
  options: { strict?: boolean } = {},
): ManifestV3 {
  let value: unknown = input;
  if (typeof input === "string") {
    try {
      value = JSON.parse(input);
    } catch (error) {
      throw new InvalidManifestError([
        {
          code: "invalid-json",
          path: "$",
          message: `manifest is not valid JSON: ${(error as Error).message}`,
          severity: "critical",
        },
      ]);
    }
  }

  const issues = validateManifest(value);
  const strict = options.strict ?? true;
  if (strict && issues.some((issue) => issue.severity !== "info")) {
    throw new InvalidManifestError(issues);
  }
  return value as ManifestV3;
}

export function normalizeWebAccessibleResources(manifest: ManifestV3): NormalizedWarEntry[] {
  return (manifest.web_accessible_resources ?? []).map((entry, index) => ({
    index,
    resources: toStringArray(entry.resources).map((resource) => resource.replace(/^\/+/, "")),
    matches: toStringArray(entry.matches),
    extensionIds: toStringArray(entry.extension_ids),
    useDynamicUrl: entry.use_dynamic_url === true,
  }));
}

export function externallyConnectableMatchesOrigin(
  manifest: ManifestV3,
  originOrExtensionId: string,
): boolean {
  const external = manifest.externally_connectable;
  if (!external) return false;
  if (isExtensionId(originOrExtensionId)) {
    return toStringArray(external.ids).includes(originOrExtensionId);
  }
  const matches = toStringArray(external.matches);
  if (matches.length === 0) return false;
  try {
    return matchAnyPattern(matches, originOrExtensionId);
  } catch {
    return false;
  }
}

export function isExtensionId(value: string): boolean {
  return /^[a-p]{32}$/.test(value);
}

export function privilegedPageResources(manifest: ManifestV3): string[] {
  const pages: string[] = [];
  const add = (value: unknown): void => {
    if (typeof value === "string" && value.length > 0) {
      pages.push(value.replace(/^\/+/, ""));
    }
  };
  add(manifest.options_ui?.page);
  add(manifest.options_page);
  add(manifest.devtools_page);
  add(manifest.action?.default_popup);
  add(manifest.browser_action?.default_popup);
  add(manifest.page_action?.default_popup);
  add(manifest.side_panel?.default_path);
  for (const value of Object.values(manifest.chrome_url_overrides ?? {})) add(value);
  return [...new Set(pages)];
}

export function isLikelyPrivilegedExtensionPage(resource: string): boolean {
  const normalized = resource.replace(/^\/+/, "").toLowerCase();
  if (!normalized.endsWith(".html") && !normalized.endsWith(".htm")) return false;
  if (/(^|\/)(public|web_accessible|assets)\//.test(normalized)) return false;
  return (
    /(options|popup|devtools|sidepanel|side_panel|admin|privileged|internal|background|dashboard|settings)\b/.test(
      normalized,
    ) || normalized === "index.html"
  );
}

export function findWarClickjackingFindings(manifest: ManifestV3): WarClickjackingFinding[] {
  const privileged = privilegedPageResources(manifest);

  const findings: WarClickjackingFinding[] = [];
  for (const entry of normalizeWebAccessibleResources(manifest)) {
    if (entry.matches.length === 0) continue;
    const exposedToAllUrls = entry.matches.some(
      (pattern) => pattern === "<all_urls>" || pattern === "*://*/*",
    );
    for (const resourceGlob of entry.resources) {
      const matchedPages = privileged.filter((page) => resourceGlobMatches(resourceGlob, page));
      if (matchedPages.length === 0 && !resourceGlob.includes("*") && isLikelyPrivilegedExtensionPage(resourceGlob)) {
        matchedPages.push(resourceGlob);
      }
      for (const page of matchedPages) {
        findings.push({
          entryIndex: entry.index,
          privilegedPage: page,
          exposedResourceGlob: resourceGlob,
          webMatches: [...entry.matches],
          exposedToAllUrls,
          severity: exposedToAllUrls ? "critical" : "high",
        });
      }
    }
  }
  return findings;
}

export function buildAccessProfile(
  manifest: ManifestV3,
  effectivePermissions: readonly string[] = toStringArray(manifest.permissions),
): ManifestAccessProfile {
  const permissions = new Set(effectivePermissions);
  const powerfulApis = [...permissions].filter((permission) => POWERFUL_PERMISSIONS.has(permission));
  return {
    cookies: permissions.has("cookies"),
    scripting: permissions.has("scripting"),
    declarativeNetRequest:
      permissions.has("declarativeNetRequest") ||
      permissions.has("declarativeNetRequestWithHostAccess"),
    webRequest:
      permissions.has("webRequest") ||
      permissions.has("webRequestBlocking"),
    tabs: permissions.has("tabs"),
    powerfulApis,
  };
}

export function solveManifestPermissions(
  input: ManifestV3 | string | unknown,
  options: PermissionSolverOptions = {},
): PermissionSolution {
  const manifest = parseManifest(input);
  const issues = validateManifest(manifest);
  const grantedOptional = new Set(options.grantedOptionalPermissions ?? []);
  const grantedOptionalHosts = new Set(options.grantedOptionalHostPermissions ?? []);

  const mandatoryPermissions = toStringArray(manifest.permissions);
  const optionalPermissions = toStringArray(manifest.optional_permissions);
  const apiPermissions = [
    ...mandatoryPermissions,
    ...optionalPermissions.filter((permission) => grantedOptional.has(permission)),
  ];

  const hostPermissions: HostPermissionGrant[] = [];
  for (const pattern of toStringArray(manifest.host_permissions)) {
    hostPermissions.push({
      pattern,
      source: "host_permissions",
      optional: false,
      granted: true,
    });
  }
  for (const pattern of toStringArray(manifest.optional_host_permissions)) {
    hostPermissions.push({
      pattern,
      source: "optional_host_permissions",
      optional: true,
      granted: grantedOptionalHosts.has(pattern),
    });
  }

  const grantedHostPatterns = hostPermissions
    .filter((grant) => grant.granted)
    .map((grant) => grant.pattern);
  const allHostPatterns = hostPermissions.map((grant) => grant.pattern);

  const warEntries = normalizeWebAccessibleResources(manifest);
  const privilegedPages = privilegedPageResources(manifest);
  const warClickjackingFindings = findWarClickjackingFindings(manifest);
  const dnrRuleResources = manifest.declarative_net_request?.rule_resources ?? [];

  const apiPermissionStatus = (permission: string): ApiPermissionStatus => {
    if (mandatoryPermissions.includes(permission)) {
      return { permission, declared: true, optional: false, granted: true };
    }
    if (optionalPermissions.includes(permission)) {
      return {
        permission,
        declared: true,
        optional: true,
        granted: grantedOptional.has(permission),
      };
    }
    return { permission, declared: false, optional: false, granted: false };
  };

  const isHostPermitted = (url: string | URL): boolean => {
    const urlString = typeof url === "string" ? url : url.href;
    return grantedHostPatterns.some((pattern) => {
      try {
        return matchPattern(pattern, urlString);
      } catch {
        return false;
      }
    });
  };

  const isContentScriptInjected = (url: string | URL): boolean =>
    contentScriptsMatching(manifest, url).length > 0;

  const warExposuresForOrigin = (resource: string, origin: string): WarExposure[] => {
    const normalized = resource.replace(/^\/+/, "");
    const exposures: WarExposure[] = [];
    for (const entry of warEntries) {
      if (!entry.resources.some((glob) => resourceGlobMatches(glob, normalized))) continue;
      if (entry.matches.length > 0) {
        try {
          if (matchAnyPattern(entry.matches, origin)) {
            exposures.push({
              entryIndex: entry.index,
              resource: normalized,
              origin,
              matches: entry.matches,
              extensionIds: entry.extensionIds,
              useDynamicUrl: entry.useDynamicUrl,
            });
          }
        } catch {}
      }
      if (isExtensionId(origin) && entry.extensionIds.includes(origin)) {
        exposures.push({
          entryIndex: entry.index,
          resource: normalized,
          extensionId: origin,
          matches: entry.matches,
          extensionIds: entry.extensionIds,
          useDynamicUrl: entry.useDynamicUrl,
        });
      }
    }
    return exposures;
  };

  const isWebAccessibleForOrigin = (resource: string, origin: string): boolean =>
    warExposuresForOrigin(resource, origin).length > 0;

  return {
    manifest,
    apiPermissions,
    optionalApiPermissions: optionalPermissions,
    hostPermissions,
    hostPermissionPatterns: allHostPatterns,
    contentScriptsForUrl: (url) => contentScriptsMatching(manifest, url),
    isHostPermitted,
    isContentScriptInjected,
    isExternallyConnectable: (originOrExtensionId) =>
      externallyConnectableMatchesOrigin(manifest, originOrExtensionId),
    externallyConnectable: manifest.externally_connectable,
    webAccessibleResources: warEntries,
    isWebAccessibleForOrigin,
    warExposuresForOrigin,
    privilegedPages,
    isPrivilegedPage: (resource) => {
      const normalized = resource.replace(/^\/+/, "");
      return privilegedPages.includes(normalized) || isLikelyPrivilegedExtensionPage(normalized);
    },
    warClickjackingFindings,
    dnrRuleResources,
    accessProfile: buildAccessProfile(manifest, apiPermissions),
    apiPermissionStatus,
    hasApiPermission: (permission) => apiPermissionStatus(permission).granted,
    issues,
  };
}

export function urlMatchesAnyHostPermission(
  manifest: ManifestV3,
  url: string | URL,
  includeOptional = false,
): boolean {
  const patterns = [
    ...toStringArray(manifest.host_permissions),
    ...(includeOptional ? toStringArray(manifest.optional_host_permissions) : []),
  ];
  const urlString = typeof url === "string" ? url : url.href;
  return patterns.some((pattern) => {
    try {
      return matchPattern(pattern, urlString);
    } catch {
      return false;
    }
  });
}

export function originFromTarget(target: string, targetKind: "url" | "domain" | "pattern" = "url"):
  | { origin?: string; pattern?: string; host?: string } {
  if (targetKind === "domain") {
    const host = target.replace(/^\*\./, "").replace(/^\./, "").toLowerCase();
    return { pattern: `*://*.${host}/*`, host };
  }
  if (targetKind === "pattern") {
    const parsed = parseMatchPattern(target);
    return { pattern: parsed.raw, host: parsed.hostSuffix ?? parsed.host };
  }
  const parsed = safeUrl(target);
  if (!parsed) return {};
  return { origin: parsed.origin === "null" ? undefined : parsed.origin, host: parsed.hostname };
}
