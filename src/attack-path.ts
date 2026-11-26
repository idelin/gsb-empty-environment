import { findWarClickjackingFindings, solveManifestPermissions } from "./manifest.ts";
import { matchAnyPattern, originOf, safeUrl } from "./match-pattern.ts";
import { manifestAllowsOperation } from "./privileges.ts";
import {
  isSensitivePrivilegedOperation,
  senderMatchesTabPermissions,
  targetMatchesPermissionPatterns,
  targetWithinSenderOriginBoundary,
} from "./targets.ts";
import type {
  AttackEntryPoint,
  AttackPath,
  AttackPathHop,
  AttackSearchResult,
  ManifestV3,
  MessageRoute,
  PrivilegedOperation,
  RuntimeWorld,
  Severity,
  TokenBindingField,
} from "./types.ts";

export interface ConfusedDeputySearchInput {
  manifest: ManifestV3;
  routes: readonly MessageRoute[];
  entries?: AttackEntryPoint[];
  untrustedOrigins?: string[];
  maxHops?: number;
  maxPaths?: number;
}

interface EntryState {
  id: string;
  origin: string;
  tabPermissions: string[];
  via: AttackEntryPoint["via"];
  resource?: string;
  messageTypes: string[];
  world: RuntimeWorld;
}

interface SearchState {
  entry: EntryState;
  world: RuntimeWorld;
  handler?: string;
  senderBound: boolean;
  visitedRouteIds: string[];
  hops: AttackPathHop[];
  missingControls: string[];
}

const REQUIRED_TOKEN_BINDINGS: readonly TokenBindingField[] = [
  "origin",
  "tabId",
  "action",
  "permissions",
];

function toStringArray(value: string | string[] | undefined): string[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? [...value] : [value];
}

function unique(values: readonly string[]): string[] {
  return [...new Set(values)];
}

function originPermissionPattern(origin: string): string {
  const parsed = safeUrl(origin);
  if (!parsed) return origin;
  if (parsed.protocol === "file:") return "file:///*";
  return `${parsed.protocol}//${parsed.hostname}/*`;
}

function normalizeUntrustedOrigin(origin: string): string | undefined {
  const value = originOf(origin);
  if (!value) return undefined;
  const parsed = safeUrl(value);
  if (!parsed || (parsed.protocol !== "http:" && parsed.protocol !== "https:")) return undefined;
  return value;
}

function externalConnectableAllows(manifest: ManifestV3, origin: string): boolean {
  const external = manifest.externally_connectable;
  if (!external) return false;
  const patterns = toStringArray(external.matches);
  if (patterns.length === 0) return false;
  try {
    return matchAnyPattern(patterns, origin);
  } catch {
    return false;
  }
}

function warAllows(manifest: ManifestV3, origin: string, resource?: string): boolean {
  const findings = findWarClickjackingFindings(manifest);
  if (!resource) {
    return findings.some((finding) => {
      try {
        return matchAnyPattern(finding.webMatches, origin);
      } catch {
        return false;
      }
    });
  }
  return findings.some((finding) => {
    try {
      return (
        finding.privilegedPage === resource.replace(/^\/+/, "") &&
        matchAnyPattern(finding.webMatches, origin)
      );
    } catch {
      return false;
    }
  });
}

function makeEntry(
  origin: string,
  route: MessageRoute,
  via: AttackEntryPoint["via"],
  tabPermissions?: string[],
  resource?: string,
): EntryState {
  const messageTypes = toStringArray(route.messageType);
  return {
    id: `${origin}:${via ?? "unknown"}:${messageTypes.join(",")}`,
    origin,
    tabPermissions: tabPermissions && tabPermissions.length > 0
      ? [...tabPermissions]
      : [originPermissionPattern(origin)],
    via,
    resource,
    messageTypes,
    world: via === "web-accessible-resource" ? "extension-page" : "page",
  };
}

function deriveEntries(
  manifest: ManifestV3,
  routes: readonly MessageRoute[],
  origins: readonly string[],
): EntryState[] {
  const entries: EntryState[] = [];
  for (const origin of origins) {
    const normalized = normalizeUntrustedOrigin(origin);
    if (!normalized) continue;
    for (const route of routes) {
      if (route.from !== "page") continue;
      if (route.to === "service-worker") {
        if (!externalConnectableAllows(manifest, normalized)) continue;
        entries.push(makeEntry(normalized, route, "externally-connectable"));
      } else if (route.to === "content-script") {
        entries.push(makeEntry(normalized, route, "content-script"));
      } else if (route.to === "extension-page") {
        if (!warAllows(manifest, normalized)) continue;
        entries.push(makeEntry(normalized, route, "web-accessible-resource"));
      } else {
        entries.push(makeEntry(normalized, route, "unknown"));
      }
    }
  }
  return dedupeEntries(entries);
}

function entriesFromInput(
  manifest: ManifestV3,
  inputEntries: readonly AttackEntryPoint[],
): EntryState[] {
  const entries: EntryState[] = [];
  for (const input of inputEntries) {
    const origin = normalizeUntrustedOrigin(input.pageOrigin);
    if (!origin) continue;
    const via = input.via;
    const messageTypes = toStringArray(input.messageType);
    const world = via === "web-accessible-resource" ? "extension-page" : "page";

    if (via === "web-accessible-resource") {
      if (!warAllows(manifest, origin, input.resource)) continue;
    }
    if (via === "externally-connectable" && !externalConnectableAllows(manifest, origin)) {
      continue;
    }

    entries.push({
      id: input.id ?? `${origin}:${via ?? "unknown"}:${messageTypes.join(",")}`,
      origin,
      tabPermissions:
        input.tabPermissions && input.tabPermissions.length > 0
          ? [...input.tabPermissions]
          : [originPermissionPattern(origin)],
      via,
      resource: input.resource,
      messageTypes,
      world,
    });
  }
  return dedupeEntries(entries);
}

function dedupeEntries(entries: readonly EntryState[]): EntryState[] {
  const seen = new Set<string>();
  const result: EntryState[] = [];
  for (const entry of entries) {
    const key = `${entry.id}|${entry.origin}|${entry.world}|${entry.tabPermissions.join(",")}`;
    if (seen.has(key)) continue;
    seen.add(key);
    result.push(entry);
  }
  return result;
}

function routeMessageType(route: MessageRoute): string {
  const types = toStringArray(route.messageType);
  return types.join("|");
}

function firstHopAllowed(route: MessageRoute, entry: EntryState): boolean {
  if (route.from !== entry.world) return false;
  if (entry.messageTypes.length > 0) {
    const routeTypes = toStringArray(route.messageType);
    if (!routeTypes.some((type) => entry.messageTypes.includes(type))) return false;
  }
  if (entry.world !== "page") return true;
  if (entry.via === "content-script" && route.to !== "content-script") return false;
  if (entry.via === "externally-connectable" && route.to !== "service-worker") return false;
  return true;
}

interface EdgeEvaluation {
  blocked: boolean;
  missingControls: string[];
  nextSenderBound: boolean;
  sinkReachable: boolean;
  sinkSeverity?: Severity;
}

function evaluateEdge(
  route: MessageRoute,
  state: SearchState,
  manifest: ManifestV3,
): EdgeEvaluation {
  const missing: string[] = [];
  const originCheck = route.originCheck ?? "none";
  const binding = route.senderBinding ?? "none";
  const operation = route.operation;
  const targetScope = route.targetScope ?? "none";
  const token = route.capabilityToken ?? { mode: "none" };

  let nextSenderBound = state.senderBound;
  if (route.from === "page" || route.from === "content-script") {
    if (originCheck === "none") {
      missing.push("origin-check-missing");
    } else if (originCheck === "presence-only") {
      missing.push("origin-check-presence-only");
    } else if (originCheck === "same-origin") {
      nextSenderBound = true;
    } else {
      try {
        if (!matchAnyPattern(originCheck.patterns, state.entry.origin)) {
          return {
            blocked: true,
            missingControls: [],
            nextSenderBound,
            sinkReachable: false,
          };
        }
        nextSenderBound = true;
      } catch {
        return {
          blocked: true,
          missingControls: [],
          nextSenderBound,
          sinkReachable: false,
        };
      }
    }
  }

  if (route.to === "service-worker" && route.from !== "service-worker") {
    if (binding === "payload-origin") {
      missing.push("sender-binding-payload-origin");
      nextSenderBound = false;
    } else if (binding !== "verified") {
      missing.push("sender-binding-missing");
      nextSenderBound = false;
    } else {
      nextSenderBound = true;
    }
  }

  if (!operation) {
    return {
      blocked: false,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  if (!isSensitivePrivilegedOperation(operation.kind)) {
    return {
      blocked: false,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  if (!manifestAllowsOperation(manifest, operation)) {
    return {
      blocked: false,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  const crossOrigin = !targetWithinSenderOriginBoundary(operation, state.entry.origin);
  if (!crossOrigin) {
    return {
      blocked: false,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  if (route.tabPermissionBound !== true) {
    missing.push("tab-permission-not-bound");
  } else if (!senderMatchesTabPermissions(state.entry.origin, state.entry.tabPermissions)) {
    return {
      blocked: true,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  let strongToken = false;
  if (token.mode === "none") {
    missing.push("capability-token-missing");
  } else if (token.mode === "static") {
    missing.push("capability-token-static");
  } else {
    const required = [...REQUIRED_TOKEN_BINDINGS];
    if (operation.target !== undefined) required.push("target");
    const absent = required.filter((field) => !(token.boundTo ?? []).includes(field));
    if (absent.length > 0) {
      missing.push(`capability-token-binding-incomplete:${absent.join("+")}`);
    } else {
      strongToken = operation.target !== undefined;
    }
  }

  if (strongToken) {
    return {
      blocked: true,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  }

  if (targetScope === "none") {
    missing.push("target-scope-missing");
  } else if (targetScope === "sender-origin") {
    if (state.senderBound || nextSenderBound) {
      return {
        blocked: true,
        missingControls: unique(missing),
        nextSenderBound,
        sinkReachable: false,
      };
    }
    missing.push("target-scope-unenforceable-without-sender-origin");
  } else if (targetScope === "tab-permissions") {
    if (
      route.tabPermissionBound === true &&
      !targetMatchesPermissionPatterns(operation, state.entry.tabPermissions)
    ) {
      return {
        blocked: true,
        missingControls: unique(missing),
        nextSenderBound,
        sinkReachable: false,
      };
    }
    return {
      blocked: true,
      missingControls: unique(missing),
      nextSenderBound,
      sinkReachable: false,
    };
  } else {
    if (!targetMatchesPermissionPatterns(operation, targetScope.patterns)) {
      return {
        blocked: true,
        missingControls: unique(missing),
        nextSenderBound,
        sinkReachable: false,
      };
    }
    missing.push("target-allowlist-does-not-bind-sender");
  }

  return {
    blocked: false,
    missingControls: unique(missing),
    nextSenderBound,
    sinkReachable: true,
    sinkSeverity: operationSeverity(operation),
  };
}

function operationSeverity(operation: PrivilegedOperation): Severity {
  switch (operation.kind) {
    case "cookie-read":
    case "script-injection":
    case "dnr-bypass":
      return "critical";
    case "fetch-proxy":
    case "storage-read":
    case "custom":
      return "high";
    default:
      return "medium";
  }
}

export function searchConfusedDeputyPaths(input: ConfusedDeputySearchInput): AttackSearchResult {
  const maxHops = input.maxHops ?? 4;
  if (!Number.isInteger(maxHops) || maxHops < 1 || maxHops > 4) {
    throw new RangeError("maxHops must be an integer between 1 and 4");
  }
  const maxPaths = Math.max(1, Math.min(input.maxPaths ?? 20, 100));
  const origins = input.untrustedOrigins ?? ["https://attacker.example"];
  const entries = input.entries
    ? entriesFromInput(input.manifest, input.entries)
    : deriveEntries(input.manifest, input.routes, origins);

  const routes = [...input.routes].sort((left, right) => left.id.localeCompare(right.id));
  const queue: SearchState[] = [];
  for (const entry of entries) {
    queue.push({
      entry,
      world: entry.world,
      handler: undefined,
      senderBound: false,
      visitedRouteIds: [],
      hops: entry.via === "web-accessible-resource"
        ? [
            {
              routeId: `war:${entry.resource ?? "*"}`,
              from: "page",
              to: "extension-page",
              messageType: "web-accessible-resource",
              missingControls: ["war-privileged-page-exposed"],
            },
          ]
        : [],
      missingControls: entry.via === "web-accessible-resource"
        ? ["war-privileged-page-exposed"]
        : [],
    });
  }

  const paths: AttackPath[] = [];
  const pathKeys = new Set<string>();
  const visitedStates = new Set<string>();
  let exploredStates = 0;

  while (queue.length > 0) {
    const state = queue.shift() as SearchState;
    exploredStates += 1;
    const routeHopCount = state.hops.filter((hop) => !hop.routeId.startsWith("war:")).length;
    if (routeHopCount >= maxHops) continue;

    for (const route of routes) {
      if (route.from !== state.world) continue;
      if (state.handler !== undefined && route.fromHandler !== state.handler) continue;
      if (routeHopCount === 0 && !firstHopAllowed(route, state.entry)) continue;
      if (state.visitedRouteIds.includes(route.id)) continue;
      if (
        route.to === "service-worker" &&
        route.from === "page" &&
        !externalConnectableAllows(input.manifest, state.entry.origin)
      ) {
        continue;
      }

      const evaluation = evaluateEdge(route, state, input.manifest);
      if (evaluation.blocked) continue;

      const hop: AttackPathHop = {
        routeId: route.id,
        from: route.from,
        to: route.to,
        messageType: routeMessageType(route),
        missingControls: evaluation.missingControls,
      };
      const next: SearchState = {
        entry: state.entry,
        world: route.to,
        handler: route.toHandler,
        senderBound: evaluation.nextSenderBound,
        visitedRouteIds: [...state.visitedRouteIds, route.id],
        hops: [...state.hops, hop],
        missingControls: unique([...state.missingControls, ...evaluation.missingControls]),
      };

      if (evaluation.sinkReachable && route.operation) {
        const path = toAttackPath(state.entry, next, route.operation, route.id, evaluation.sinkSeverity ?? "high");
        const key = `${path.entryOrigin}|${path.sinkRouteId}|${path.hops.map((entry) => entry.routeId).join(">")}`;
        if (!pathKeys.has(key) && paths.length < maxPaths) {
          pathKeys.add(key);
          paths.push(path);
        }
      }

      const nextRouteHopCount = next.hops.filter((entry) => !entry.routeId.startsWith("war:")).length;
      if (nextRouteHopCount < maxHops) {
        const stateKey = `${next.entry.id}|${next.world}|${next.handler ?? ""}|${next.senderBound}|${next.visitedRouteIds.join(">")}`;
        if (!visitedStates.has(stateKey)) {
          visitedStates.add(stateKey);
          queue.push(next);
        }
      }
    }
  }

  paths.sort((left, right) => {
    if (left.hopCount !== right.hopCount) return left.hopCount - right.hopCount;
    if (left.entryOrigin !== right.entryOrigin) return left.entryOrigin.localeCompare(right.entryOrigin);
    return left.hops.map((hop) => hop.routeId).join(">").localeCompare(
      right.hops.map((hop) => hop.routeId).join(">"),
    );
  });

  return {
    maxHops,
    paths,
    bestPath: paths[0],
    exploredStates,
  };
}

function toAttackPath(
  entry: EntryState,
  state: SearchState,
  sink: PrivilegedOperation,
  sinkRouteId: string,
  severity: Severity,
): AttackPath {
  return {
    entryOrigin: entry.origin,
    entryId: entry.id,
    sink,
    sinkRouteId,
    hopCount: state.hops.filter((hop) => !hop.routeId.startsWith("war:")).length,
    hops: state.hops,
    missingControls: unique(state.missingControls),
    severity,
  };
}
