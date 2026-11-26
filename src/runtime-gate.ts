import { CapabilityAuthority, type CapabilityAuthorityOptions } from "./capability-token.ts";
import {
  isWebOrigin,
  matchAnyPattern,
  originOf as normalizeOriginFromMatch,
} from "./match-pattern.ts";
import { findDnrPriorityIssues } from "./declarative-net-request.ts";
import { findWarClickjackingFindings, normalizeWebAccessibleResources } from "./manifest.ts";
import { manifestAllowsOperation } from "./privileges.ts";
import type {
  AuthorizationDecision,
  CapabilityTokenClaims,
  ManifestV3,
  MessageRoute,
  PrivilegedOperation,
  RuntimeAuthorizationRequest,
  SecurityIssue,
  Severity,
  TokenBindingField,
} from "./types.ts";
import {
  describePrivilegedTarget,
  isSensitivePrivilegedOperation,
  senderMatchesTabPermissions,
  targetMatchesPermissionPatterns,
  targetWithinSenderOriginBoundary,
} from "./targets.ts";

export interface RuntimeSecurityReviewInput {
  manifest: ManifestV3;
  routes: readonly MessageRoute[];
  dnrRules?: unknown[];
}

export interface RuntimeGateOptions {
  manifest: ManifestV3;
  routes: readonly MessageRoute[];
  authority: CapabilityAuthority | CapabilityAuthorityOptions;
  allowCrossOriginTarget?: boolean;
}

const PRIVILEGED_TOKEN_BINDINGS: readonly TokenBindingField[] = [
  "origin",
  "tabId",
  "action",
  "permissions",
];

function toStringArray(value: string | string[] | undefined): string[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? [...value] : [value];
}

function issue(
  code: SecurityIssue["code"],
  severity: Severity,
  message: string,
  extras: Pick<SecurityIssue, "routeId" | "resource" | "evidence"> = {},
): SecurityIssue {
  return { code, severity, message, ...extras };
}

export function verifyMessageRouteSecurity(route: MessageRoute): SecurityIssue[] {
  const issues: SecurityIssue[] = [];
  const originCheck = route.originCheck ?? "none";
  const token = route.capabilityToken ?? { mode: "none" };
  const targetScope = route.targetScope ?? "none";
  const operation = route.operation;

  if (route.from === "page" || route.from === "content-script") {
    if (originCheck === "none") {
      issues.push(
        issue("MISSING_ORIGIN_CHECK", "critical", `route ${route.id} forwards page messages without checking event.origin`, {
          routeId: route.id,
        }),
      );
    } else if (originCheck === "presence-only") {
      issues.push(
        issue(
          "ORIGIN_CHECK_PRESENCE_ONLY",
          "high",
          `route ${route.id} only tests that origin exists; it must bind the origin to the expected sender`,
          { routeId: route.id },
        ),
      );
    } else if (typeof originCheck === "object") {
      if (originCheck.patterns.length === 0) {
        issues.push(
          issue("ORIGIN_CHECK_UNTRUSTED", "high", `route ${route.id} has an empty origin allowlist`, {
            routeId: route.id,
          }),
        );
      } else if (
        originCheck.patterns.some((pattern) => pattern === "<all_urls>" || pattern === "*://*/*")
      ) {
        issues.push(
          issue(
            "ORIGIN_ALLOWLIST_TOO_BROAD",
            "medium",
            `route ${route.id} accepts every web origin as a trusted sender`,
            { routeId: route.id, evidence: { patterns: originCheck.patterns } },
          ),
        );
      }
    }
  }

  if (route.to === "service-worker" && route.from !== "service-worker") {
    const binding = route.senderBinding ?? "none";
    if (binding === "payload-origin") {
      issues.push(
        issue(
          "PAYLOAD_ORIGIN_SPOOFABLE",
          "critical",
          `route ${route.id} takes sender origin from the message payload, which the page can forge`,
          { routeId: route.id },
        ),
      );
    } else if (binding !== "verified") {
      issues.push(
        issue(
          "SENDER_ORIGIN_UNBOUND",
          "critical",
          `route ${route.id} reaches the service worker without a verified sender.origin binding`,
          { routeId: route.id },
        ),
      );
    }
  }

  if (operation && isSensitivePrivilegedOperation(operation.kind)) {
    if (route.tabPermissionBound !== true) {
      issues.push(
        issue(
          "TAB_PERMISSION_NOT_BOUND",
          "high",
          `route ${route.id} invokes ${operation.kind} without binding the live tab permission set`,
          { routeId: route.id },
        ),
      );
    }
    if (token.mode === "none") {
      issues.push(
        issue(
          "CAPABILITY_TOKEN_MISSING",
          "high",
          `route ${route.id} invokes ${operation.kind} without an action-level capability token`,
          { routeId: route.id },
        ),
      );
    } else if (token.mode === "static") {
      issues.push(
        issue(
          "CAPABILITY_TOKEN_STATIC",
          "high",
          `route ${route.id} uses a static/shared token that page content can replay`,
          { routeId: route.id },
        ),
      );
    } else if (token.mode === "signed") {
      const boundTo = token.boundTo ?? [];
      const required = [...PRIVILEGED_TOKEN_BINDINGS];
      if (operation.target !== undefined) required.push("target");
      const missing = required.filter((field) => !boundTo.includes(field));
      if (missing.length > 0) {
        issues.push(
          issue(
            "CAPABILITY_TOKEN_BINDING_INCOMPLETE",
            "high",
            `route ${route.id} token is not bound to ${missing.join(", ")}`,
            { routeId: route.id, evidence: { missing, boundTo } },
          ),
        );
      }
    }

    if (targetScope === "none") {
      issues.push(
        issue(
          "TARGET_SCOPE_MISSING",
          "high",
          `route ${route.id} invokes ${operation.kind} without constraining the target to the sender's origin/tab scope`,
          { routeId: route.id },
        ),
      );
    }

    if (route.expectedPageOrigins && operation.target) {
      for (const origin of route.expectedPageOrigins) {
        if (!targetWithinSenderOriginBoundary(operation, origin)) {
          issues.push(
            issue(
              "TARGET_OUTSIDE_TAB_ORIGIN",
              "critical",
              `route ${route.id} lets ${origin} reach ${operation.kind} for a target outside its origin boundary`,
              {
                routeId: route.id,
                evidence: { origin, operation: operation.kind, target: operation.target },
              },
            ),
          );
          break;
        }
      }
    }
  }

  return issues;
}

export function analyzeManifestSecurity(
  manifest: ManifestV3,
  routes: readonly MessageRoute[] = [],
  dnrRules?: unknown[],
): SecurityIssue[] {
  const issues: SecurityIssue[] = [];

  for (const finding of findWarClickjackingFindings(manifest)) {
    issues.push(
      issue(
        "WAR_PRIVILEGED_PAGE_EXPOSED",
        finding.severity,
        `web_accessible_resources exposes internal extension page ${finding.privilegedPage}; a matching web page can frame it`,
        {
          resource: finding.privilegedPage,
          evidence: {
            exposedResourceGlob: finding.exposedResourceGlob,
            webMatches: finding.webMatches,
          },
        },
      ),
    );
  }

  for (const entry of normalizeWebAccessibleResources(manifest)) {
    if (entry.matches.length === 0) continue;
    const broadMatches = entry.matches.some(
      (pattern) => pattern === "<all_urls>" || pattern === "*://*/*",
    );
    if (!broadMatches) continue;
    for (const resource of entry.resources) {
      if (/\.html?$/.test(resource) || /\*\.html?$/.test(resource)) {
        issues.push(
          issue(
            "WAR_BROAD_EXPOSURE",
            "high",
            `web_accessible_resources exposes HTML resources to every web origin (${resource})`,
            { resource, evidence: { matches: entry.matches } },
          ),
        );
      }
    }
  }

  const external = manifest.externally_connectable;
  if (external) {
    const matches = toStringArray(external.matches);
    if (matches.some((pattern) => pattern === "*://*/*" || pattern === "<all_urls>")) {
      issues.push(
        issue(
          "EXTERNALLY_CONNECTABLE_WILDCARD",
          "high",
          "externally_connectable matches every web origin; any page can message the service worker",
          { evidence: { matches } },
        ),
      );
    }
    if (matches.length > 0 && toStringArray(external.ids).length === 0) {
      const privilegedRoutes = routes.filter(
        (route) => route.operation && isSensitivePrivilegedOperation(route.operation.kind),
      );
      if (privilegedRoutes.length > 0) {
        issues.push(
          issue(
            "EXTERNALLY_CONNECTABLE_UNSCOPED",
            "medium",
            "externally_connectable exposes privileged message routes to web origins",
            {
              evidence: {
                matches,
                privilegedRouteIds: privilegedRoutes.map((route) => route.id),
              },
            },
          ),
        );
      }
    }
  }

  if (dnrRules !== undefined) {
    let priorityIssues: ReturnType<typeof findDnrPriorityIssues>;
    try {
      priorityIssues = findDnrPriorityIssues(dnrRules);
    } catch {
      priorityIssues = [];
    }
    for (const priorityIssue of priorityIssues) {
      issues.push(
        issue(
          priorityIssue.kind === "allowAllRequests"
            ? "DNR_ALLOW_ALL_REQUESTS_OVERRIDE"
            : "DNR_RULE_PRIORITY_OVERRIDE",
          priorityIssue.kind === "allowAllRequests" ? "critical" : "high",
          priorityIssue.message,
          {
            evidence: {
              overridingRuleId: priorityIssue.overridingRuleId,
              blockedRuleIds: priorityIssue.potentiallyBlockedRuleIds,
            },
          },
        ),
      );
    }
  }

  return issues;
}

export function reviewRuntimeSecurity(input: RuntimeSecurityReviewInput): {
  issues: SecurityIssue[];
  routeIssues: Map<string, SecurityIssue[]>;
  safe: boolean;
} {
  const routeIssues = new Map<string, SecurityIssue[]>();
  for (const route of input.routes) {
    routeIssues.set(route.id, verifyMessageRouteSecurity(route));
  }
  const issues = [
    ...input.routes.flatMap((route) => routeIssues.get(route.id) ?? []),
    ...analyzeManifestSecurity(input.manifest, input.routes, input.dnrRules),
  ];
  return { issues, routeIssues, safe: !issues.some((entry) => entry.severity === "critical" || entry.severity === "high") };
}

export class RuntimeMessageGate {
  private readonly routes: Map<string, MessageRoute>;
  private readonly authority: CapabilityAuthority;
  private readonly allowCrossOriginTarget: boolean;
  private readonly manifest: ManifestV3;

  constructor(options: RuntimeGateOptions) {
    this.routes = new Map(options.routes.map((route) => [route.id, route]));
    this.manifest = options.manifest;
    this.authority =
      options.authority instanceof CapabilityAuthority
        ? options.authority
        : new CapabilityAuthority(options.authority);
    this.allowCrossOriginTarget = options.allowCrossOriginTarget === true;
  }

  listRoutes(): MessageRoute[] {
    return [...this.routes.values()];
  }

  async issueToken(
    routeId: string,
    input: Omit<Parameters<CapabilityAuthority["issueToken"]>[0], "routeId" | "actionId">,
  ): Promise<string> {
    const route = this.routes.get(routeId);
    if (!route) throw new Error(`unknown route ${routeId}`);
    return this.authority.issueToken({ ...input, routeId, actionId: route.actionId ?? route.id }, route);
  }

  async authorize(request: RuntimeAuthorizationRequest): Promise<AuthorizationDecision> {
    const route = this.routes.get(request.routeId);
    if (!route) return deny("UNKNOWN_ROUTE", `unknown route ${request.routeId}`);

    const messageTypes = toStringArray(route.messageType);
    if (!messageTypes.includes(request.messageType)) {
      return deny("MESSAGE_TYPE_MISMATCH", `message type ${request.messageType} is not declared for ${route.id}`);
    }

    const payloadOrigin = readPayloadOrigin(request.payload);
    const rawOrigin = request.sender.origin;
    if (!rawOrigin) return deny("MISSING_SENDER_ORIGIN", "sender.origin is required and must come from the trusted runtime");
    if (rawOrigin === "null") return deny("OPAQUE_SENDER_ORIGIN", "opaque/null web origins cannot receive privileged capabilities");
    const origin = normalizeOriginFromMatch(rawOrigin);
    if (!origin) return deny("MISSING_SENDER_ORIGIN", `sender origin ${JSON.stringify(rawOrigin)} is not an absolute origin`);

    if (payloadOrigin !== undefined) {
      const normalizedPayloadOrigin = normalizeOriginFromMatch(payloadOrigin);
      if (!normalizedPayloadOrigin || normalizedPayloadOrigin !== origin) {
        return deny(
          "PAYLOAD_ORIGIN_SPOOFED",
          "message payload attempted to override the trusted sender.origin",
        );
      }
    }

    const originCheck = route.originCheck ?? "none";
    if (originCheck === "none" || originCheck === "presence-only") {
      return deny(
        originCheck === "none" ? "ORIGIN_CHECK_MISSING" : "ORIGIN_CHECK_PRESENCE_ONLY",
        `route ${route.id} does not bind sender.origin to an expected page`,
      );
    }
    if (originCheck === "same-origin") {
      if (!request.sender.tabUrl) return deny("MISSING_TAB_CONTEXT", "same-origin checks require sender.tabUrl");
      const tabOrigin = normalizeOriginFromMatch(request.sender.tabUrl);
      if (!tabOrigin || tabOrigin !== origin) {
        return deny("ORIGIN_SCOPE_VIOLATION", "sender.origin does not match the current tab origin");
      }
    } else {
      try {
        if (!matchAnyPattern(originCheck.patterns, origin)) {
          return deny("ORIGIN_SCOPE_VIOLATION", "sender.origin is outside the route allowlist");
        }
      } catch {
        return deny("ORIGIN_SCOPE_VIOLATION", "route contains an invalid origin allowlist pattern");
      }
    }

    const binding = route.senderBinding ?? "none";
    const needsSenderBinding = route.from === "page" || route.from === "content-script";
    if (needsSenderBinding && binding !== "verified") {
      return deny(
        "SENDER_BINDING_MISSING",
        binding === "payload-origin"
          ? "sender origin is derived from page-controlled payload"
          : "sender origin is not bound by the runtime",
      );
    }

    if (route.operation && isSensitivePrivilegedOperation(route.operation.kind)) {
      if (!Number.isInteger(request.sender.tabId) || (request.sender.tabId as number) < 0) {
        return deny("MISSING_TAB_CONTEXT", "privileged routes require a trusted tabId");
      }
      const permissions = request.sender.tabPermissions;
      if (!permissions || permissions.length === 0) {
        return deny("MISSING_TAB_CONTEXT", "privileged routes require the live tab permission set");
      }
      if (route.tabPermissionBound !== true) {
        return deny("TAB_PERMISSION_NOT_BOUND", `route ${route.id} does not bind the tab permission set`);
      }
      if (!senderMatchesTabPermissions(origin, permissions)) {
        return deny("SENDER_OUTSIDE_TAB_PERMISSIONS", "sender.origin is not covered by the live tab permission set");
      }
    }

    const tokenPolicy = route.capabilityToken ?? { mode: "none" };
    let tokenClaims: CapabilityTokenClaims | undefined;
    if (route.operation && isSensitivePrivilegedOperation(route.operation.kind)) {
      if (tokenPolicy.mode === "none") {
        return deny("CAPABILITY_TOKEN_REQUIRED", `route ${route.id} requires an action-level capability token`);
      }
      if (tokenPolicy.mode === "static") {
        return deny("CAPABILITY_TOKEN_STATIC", "static shared tokens are replayable from page content");
      }
      const boundTo = tokenPolicy.boundTo ?? [];
      const required = [...PRIVILEGED_TOKEN_BINDINGS];
      if (route.operation.target !== undefined) required.push("target");
      if (!required.every((field) => boundTo.includes(field))) {
        return deny(
          "CAPABILITY_TOKEN_BINDING_INCOMPLETE",
          `token policy is not bound to ${required.filter((field) => !boundTo.includes(field)).join(", ")}`,
        );
      }
      if (!request.token) return deny("CAPABILITY_TOKEN_REQUIRED", "missing capability token");
      const verification = await this.authority.verifyToken(request.token, {
        routeId: route.id,
        actionId: route.actionId ?? route.id,
        origin,
        tabId: request.sender.tabId as number,
        tabPermissions: request.sender.tabPermissions,
        operation: route.operation,
      });
      if (!verification.valid) {
        return deny("CAPABILITY_TOKEN_INVALID", `token rejected: ${verification.reason} (${verification.detail})`);
      }
      tokenClaims = verification.claims;
    }

    if (route.operation && isSensitivePrivilegedOperation(route.operation.kind)) {
      if (!manifestAllowsOperation(this.manifest, route.operation)) {
        return deny(
          "OPERATION_NOT_GRANTED",
          `manifest does not grant ${route.operation.kind} for the declared target`,
        );
      }
      const targetScope = route.targetScope ?? "none";
      if (targetScope === "none") {
        return deny("TARGET_SCOPE_MISSING", `route ${route.id} does not constrain ${route.operation.kind} target`);
      }

      if (targetScope === "sender-origin") {
        if (!targetWithinSenderOriginBoundary(route.operation, origin)) {
          return deny(
            "TARGET_OUTSIDE_ORIGIN_BOUNDARY",
            `${route.operation.kind} target is outside ${origin}`,
          );
        }
      } else if (targetScope === "tab-permissions") {
        if (!targetMatchesPermissionPatterns(route.operation, request.sender.tabPermissions ?? [])) {
          return deny("TARGET_NOT_IN_TAB_PERMISSIONS", `${route.operation.kind} target is outside the tab grant`);
        }
        if (!this.allowCrossOriginTarget && !targetWithinSenderOriginBoundary(route.operation, origin)) {
          return deny(
            "TARGET_OUTSIDE_ORIGIN_BOUNDARY",
            `${route.operation.kind} target is outside ${origin} even though the tab grant contains it`,
          );
        }
      } else {
        if (!targetMatchesPermissionPatterns(route.operation, targetScope.patterns)) {
          return deny("TARGET_OUTSIDE_ALLOWLIST", `${route.operation.kind} target is outside the configured allowlist`);
        }
        if (!this.allowCrossOriginTarget && !targetWithinSenderOriginBoundary(route.operation, origin)) {
          return deny(
            "TARGET_OUTSIDE_ORIGIN_BOUNDARY",
            `${route.operation.kind} target is outside ${origin}`,
          );
        }
      }

      const descriptor = describePrivilegedTarget(route.operation);
      if (descriptor.kind === "global" && !this.allowCrossOriginTarget) {
        return deny("TARGET_SCOPE_MISSING", "global privileged operations are not allowed for page-origin callers");
      }
    }

    return {
      allowed: true,
      code: "ALLOWED",
      reason: `route ${route.id} authorized for ${origin} tab ${request.sender.tabId}`,
      tokenClaims,
    };
  }
}

function deny(code: AuthorizationDecision["code"], reason: string): AuthorizationDecision {
  return { allowed: false, code, reason };
}

function readPayloadOrigin(payload: unknown): string | undefined {
  if (payload === null || typeof payload !== "object") return undefined;
  const value = (payload as Record<string, unknown>).origin;
  return typeof value === "string" ? value : undefined;
}

export function isRouteSafe(route: MessageRoute): boolean {
  return verifyMessageRouteSecurity(route).every(
    (entry) => entry.severity !== "critical" && entry.severity !== "high",
  );
}

export function isWebOriginValue(value: string): boolean {
  return isWebOrigin(value);
}
