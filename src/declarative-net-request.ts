import { registrableDomainApprox, safeUrl } from "./match-pattern.ts";

export type DnrActionType =
  | "allowAllRequests"
  | "allow"
  | "block"
  | "redirect"
  | "upgradeScheme"
  | "modifyHeaders";

export type DnrResourceType =
  | "main_frame"
  | "sub_frame"
  | "stylesheet"
  | "script"
  | "image"
  | "font"
  | "object"
  | "xmlhttprequest"
  | "ping"
  | "csp_report"
  | "media"
  | "websocket"
  | "webtransport"
  | "webbundle"
  | "other";

export type DnrDomainType =
  | "firstParty"
  | "thirdParty"
  | "thirdPartyWithFirstParty";

export interface DnrHeaderOperation {
  header: string;
  operation: "append" | "set" | "remove";
  value?: string;
}

export interface DnrAction {
  type: DnrActionType;
  redirect?: {
    url?: string;
    extensionPath?: string;
    transform?: Record<string, unknown>;
  };
  requestHeaders?: DnrHeaderOperation[];
  responseHeaders?: DnrHeaderOperation[];
}

export interface DnrCondition {
  urlFilter?: string;
  regexFilter?: string;
  isUrlFilterCaseSensitive?: boolean;
  requestDomains?: string[];
  excludedRequestDomains?: string[];
  initiatorDomains?: string[];
  excludedInitiatorDomains?: string[];
  resourceTypes?: DnrResourceType[];
  excludedResourceTypes?: DnrResourceType[];
  requestMethods?: string[];
  excludedRequestMethods?: string[];
  domainType?: DnrDomainType;
  tabIds?: number[];
  excludedTabIds?: number[];
}

export interface DnrRule {
  id: number;
  priority?: number;
  action: DnrAction;
  condition: DnrCondition;
}

export interface DnrRequest {
  url: string;
  initiator?: string;
  method?: string;
  type?: DnrResourceType;
  tabId?: number;
  frameId?: number;
}

export interface NormalizedDnrRequest {
  url: string;
  host: string;
  initiator?: string;
  initiatorHost?: string;
  method: string;
  type: DnrResourceType;
  tabId?: number;
}

export interface MatchedDnrRule {
  rule: DnrRule;
  priority: number;
  actionPrecedence: number;
}

export interface DnrEvaluation {
  request: NormalizedDnrRequest;
  matchedRules: MatchedDnrRule[];
  winner?: MatchedDnrRule;
  action: DnrActionType | "none";
  allowed: boolean;
  blocked: boolean;
  redirect?: DnrAction["redirect"];
  modifyHeaderRules: MatchedDnrRule[];
}

export interface DnrPriorityIssue {
  kind: "allowAllRequests" | "allow";
  overridingRuleId: number;
  potentiallyBlockedRuleIds: number[];
  message: string;
}

export class InvalidDnrRuleError extends Error {
  readonly ruleIndex: number;
  readonly reason: string;

  constructor(ruleIndex: number, reason: string) {
    super(`Invalid declarativeNetRequest rule at index ${ruleIndex}: ${reason}`);
    this.name = "InvalidDnrRuleError";
    this.ruleIndex = ruleIndex;
    this.reason = reason;
  }
}

export const ACTION_PRECEDENCE: Record<DnrActionType, number> = {
  allowAllRequests: 5,
  allow: 4,
  block: 3,
  redirect: 2,
  upgradeScheme: 1.5,
  modifyHeaders: 1,
};

const ACTION_TYPES = new Set<DnrActionType>([
  "allowAllRequests",
  "allow",
  "block",
  "redirect",
  "upgradeScheme",
  "modifyHeaders",
]);

const RESOURCE_TYPES = new Set<DnrResourceType>([
  "main_frame",
  "sub_frame",
  "stylesheet",
  "script",
  "image",
  "font",
  "object",
  "xmlhttprequest",
  "ping",
  "csp_report",
  "media",
  "websocket",
  "webtransport",
  "webbundle",
  "other",
]);

export function parseDnrRules(input: unknown): DnrRule[] {
  let value: unknown = input;
  if (typeof input === "string") {
    try {
      value = JSON.parse(input);
    } catch (error) {
      throw new InvalidDnrRuleError(0, `rules are not valid JSON: ${(error as Error).message}`);
    }
  }
  if (!Array.isArray(value)) {
    throw new InvalidDnrRuleError(0, "rules must be an array");
  }

  const ids = new Set<number>();
  const rules: DnrRule[] = [];
  value.forEach((ruleValue, index) => {
    if (ruleValue === null || typeof ruleValue !== "object" || Array.isArray(ruleValue)) {
      throw new InvalidDnrRuleError(index, "rule must be an object");
    }
    const rule = ruleValue as Record<string, unknown>;
    if (typeof rule.id !== "number" || !Number.isInteger(rule.id) || rule.id <= 0) {
      throw new InvalidDnrRuleError(index, "id must be a positive integer");
    }
    if (ids.has(rule.id)) {
      throw new InvalidDnrRuleError(index, `duplicate rule id ${rule.id}`);
    }
    ids.add(rule.id);

    if (rule.priority !== undefined) {
      if (typeof rule.priority !== "number" || !Number.isInteger(rule.priority) || rule.priority < 1) {
        throw new InvalidDnrRuleError(index, "priority must be a positive integer");
      }
    }

    if (rule.action === null || typeof rule.action !== "object") {
      throw new InvalidDnrRuleError(index, "action must be an object");
    }
    const action = rule.action as Record<string, unknown>;
    if (typeof action.type !== "string" || !ACTION_TYPES.has(action.type as DnrActionType)) {
      throw new InvalidDnrRuleError(index, `unknown action type ${String(action.type)}`);
    }
    if (action.type === "redirect" && action.redirect === undefined) {
      throw new InvalidDnrRuleError(index, "redirect action requires redirect details");
    }
    if (
      action.type === "modifyHeaders" &&
      action.requestHeaders === undefined &&
      action.responseHeaders === undefined
    ) {
      throw new InvalidDnrRuleError(index, "modifyHeaders action requires requestHeaders or responseHeaders");
    }

    if (rule.condition === null || typeof rule.condition !== "object") {
      throw new InvalidDnrRuleError(index, "condition must be an object");
    }
    const condition = rule.condition as Record<string, unknown>;
    if (condition.urlFilter !== undefined && condition.regexFilter !== undefined) {
      throw new InvalidDnrRuleError(index, "urlFilter and regexFilter are mutually exclusive");
    }
    if (condition.urlFilter !== undefined && typeof condition.urlFilter !== "string") {
      throw new InvalidDnrRuleError(index, "urlFilter must be a string");
    }
    if (condition.regexFilter !== undefined) {
      if (typeof condition.regexFilter !== "string") {
        throw new InvalidDnrRuleError(index, "regexFilter must be a string");
      }
      try {
        new RegExp(condition.regexFilter);
      } catch (error) {
        throw new InvalidDnrRuleError(index, `regexFilter is invalid: ${(error as Error).message}`);
      }
    }
    for (const field of ["resourceTypes", "excludedResourceTypes"] as const) {
      const values = condition[field];
      if (values !== undefined) {
        if (!Array.isArray(values)) {
          throw new InvalidDnrRuleError(index, `${field} must be an array`);
        }
        for (const resourceType of values) {
          if (!RESOURCE_TYPES.has(resourceType as DnrResourceType)) {
            throw new InvalidDnrRuleError(index, `unknown resource type ${String(resourceType)}`);
          }
        }
      }
    }

    rules.push(rule as unknown as DnrRule);
  });
  return rules;
}

export function normalizeDnrRequest(request: DnrRequest): NormalizedDnrRequest {
  const parsed = safeUrl(request.url);
  if (!parsed) {
    throw new Error(`Invalid DNR request URL ${JSON.stringify(request.url)}`);
  }
  const initiator = request.initiator ? safeUrl(request.initiator) : undefined;
  return {
    url: parsed.href,
    host: parsed.hostname.toLowerCase(),
    initiator: initiator?.href,
    initiatorHost: initiator?.hostname.toLowerCase(),
    method: (request.method ?? "GET").toUpperCase(),
    type: request.type ?? "main_frame",
    tabId: request.tabId,
  };
}

export function urlFilterToRegExp(filter: string, caseSensitive = false): RegExp {
  let source = "";
  let rest = filter;

  if (rest.startsWith("||")) {
    source += "^[a-z][a-z0-9+.-]*://(?:[^/?#]*\\.)?";
    rest = rest.slice(2);
  } else if (rest.startsWith("|")) {
    source += "^";
    rest = rest.slice(1);
  }

  let endAnchored = false;
  if (rest.endsWith("|")) {
    endAnchored = true;
    rest = rest.slice(0, -1);
  }

  for (const char of rest) {
    if (char === "*") {
      source += ".*";
    } else if (char === "^") {
      source += "(?:[^a-zA-Z0-9_.%-]|$)";
    } else {
      source += char.replace(/[\\^$.*+?()[\]{}|]/g, "\\$&");
    }
  }
  if (endAnchored) source += "$";
  return new RegExp(source, caseSensitive ? "" : "i");
}

function domainInList(host: string, domains: readonly string[]): boolean {
  const normalizedHost = host.toLowerCase();
  return domains.some((domain) => {
    const normalizedDomain = domain.toLowerCase().replace(/^\*\./, "");
    return normalizedHost === normalizedDomain || normalizedHost.endsWith(`.${normalizedDomain}`);
  });
}

function conditionMatches(condition: DnrCondition, request: NormalizedDnrRequest): boolean {
  if (condition.urlFilter !== undefined) {
    if (!urlFilterToRegExp(condition.urlFilter, condition.isUrlFilterCaseSensitive === true).test(request.url)) {
      return false;
    }
  }
  if (condition.regexFilter !== undefined) {
    const flags = condition.isUrlFilterCaseSensitive === true ? "" : "i";
    if (!new RegExp(condition.regexFilter, flags).test(request.url)) return false;
  }

  if (condition.requestDomains && !domainInList(request.host, condition.requestDomains)) {
    return false;
  }
  if (condition.excludedRequestDomains && domainInList(request.host, condition.excludedRequestDomains)) {
    return false;
  }
  if (condition.initiatorDomains) {
    if (!request.initiatorHost || !domainInList(request.initiatorHost, condition.initiatorDomains)) {
      return false;
    }
  }
  if (
    condition.excludedInitiatorDomains &&
    request.initiatorHost &&
    domainInList(request.initiatorHost, condition.excludedInitiatorDomains)
  ) {
    return false;
  }
  if (condition.resourceTypes && !condition.resourceTypes.includes(request.type)) {
    return false;
  }
  if (condition.excludedResourceTypes && condition.excludedResourceTypes.includes(request.type)) {
    return false;
  }
  if (
    condition.requestMethods &&
    !condition.requestMethods.map((method) => method.toUpperCase()).includes(request.method)
  ) {
    return false;
  }
  if (
    condition.excludedRequestMethods &&
    condition.excludedRequestMethods.map((method) => method.toUpperCase()).includes(request.method)
  ) {
    return false;
  }
  if (condition.tabIds && (request.tabId === undefined || !condition.tabIds.includes(request.tabId))) {
    return false;
  }
  if (
    condition.excludedTabIds &&
    request.tabId !== undefined &&
    condition.excludedTabIds.includes(request.tabId)
  ) {
    return false;
  }

  if (condition.domainType) {
    const requestDomain = registrableDomainApprox(request.host);
    const initiatorDomain = request.initiatorHost
      ? registrableDomainApprox(request.initiatorHost)
      : undefined;
    if (condition.domainType === "firstParty") {
      if (initiatorDomain && requestDomain !== initiatorDomain) return false;
    } else if (condition.domainType === "thirdParty") {
      if (!initiatorDomain || requestDomain === initiatorDomain) return false;
    } else {
      if (!initiatorDomain || requestDomain === initiatorDomain) return false;
    }
  }

  return true;
}

export function evaluateDeclarativeNetRequest(
  rulesInput: readonly DnrRule[] | unknown,
  request: DnrRequest,
): DnrEvaluation {
  const rules = Array.isArray(rulesInput)
    ? (rulesInput as DnrRule[])
    : parseDnrRules(rulesInput);
  const normalizedRequest = normalizeDnrRequest(request);

  const matchedRules: MatchedDnrRule[] = [];
  for (const rule of rules) {
    if (!conditionMatches(rule.condition, normalizedRequest)) continue;
    const actionType = rule.action.type;
    if (
      actionType === "allowAllRequests" &&
      normalizedRequest.type !== "main_frame" &&
      normalizedRequest.type !== "sub_frame"
    ) {
      continue;
    }
    matchedRules.push({
      rule,
      priority: rule.priority ?? 1,
      actionPrecedence: ACTION_PRECEDENCE[actionType],
    });
  }

  matchedRules.sort((left, right) => {
    if (right.priority !== left.priority) return right.priority - left.priority;
    if (right.actionPrecedence !== left.actionPrecedence) {
      return right.actionPrecedence - left.actionPrecedence;
    }
    return left.rule.id - right.rule.id;
  });

  const winner = matchedRules[0];
  const modifyHeaderRules = matchedRules.filter((entry) => entry.rule.action.type === "modifyHeaders");
  if (!winner) {
    return {
      request: normalizedRequest,
      matchedRules,
      action: "none",
      allowed: false,
      blocked: false,
      modifyHeaderRules,
    };
  }

  const action = winner.rule.action.type;
  return {
    request: normalizedRequest,
    matchedRules,
    winner,
    action,
    allowed: action === "allow" || action === "allowAllRequests",
    blocked: action === "block",
    redirect:
      action === "redirect" || action === "upgradeScheme" ? winner.rule.action.redirect : undefined,
    modifyHeaderRules,
  };
}

export function findDnrPriorityIssues(rulesInput: readonly DnrRule[] | unknown): DnrPriorityIssue[] {
  const rules = Array.isArray(rulesInput) ? (rulesInput as DnrRule[]) : parseDnrRules(rulesInput);
  const blocks = rules.filter((rule) => rule.action.type === "block");
  const issues: DnrPriorityIssue[] = [];

  for (const rule of rules) {
    const actionType = rule.action.type;
    if (actionType !== "allowAllRequests" && actionType !== "allow") continue;
    const rulePriority = rule.priority ?? 1;
    const overridden = blocks.filter(
      (block) =>
        conditionsCanOverlap(rule.condition, block.condition) &&
        rulePriority >= (block.priority ?? 1),
    );
    if (overridden.length === 0) continue;
    issues.push({
      kind: actionType,
      overridingRuleId: rule.id,
      potentiallyBlockedRuleIds: overridden.map((block) => block.id),
      message: `${actionType} rule ${rule.id} can outrank block rule(s) ${overridden
        .map((block) => block.id)
        .join(", ")}`,
    });
  }

  return issues;
}

function conditionsCanOverlap(left: DnrCondition, right: DnrCondition): boolean {
  if (left.urlFilter !== undefined && right.urlFilter !== undefined) {
    if (left.urlFilter !== right.urlFilter) return false;
  } else if (left.urlFilter !== undefined || right.urlFilter !== undefined) {
    if (left.urlFilter === "*" || right.urlFilter === "*") return true;
    return false;
  }
  if (left.requestDomains && right.requestDomains) {
    if (!left.requestDomains.some((domain) => right.requestDomains?.includes(domain))) return false;
  }
  if (left.resourceTypes && right.resourceTypes) {
    if (!left.resourceTypes.some((type) => right.resourceTypes?.includes(type))) return false;
  }
  return true;
}
