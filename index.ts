export type CrossOriginEmbedderPolicy =
  | "unsafe-none"
  | "credentialless"
  | "require-corp";

export type CrossOriginResourcePolicy =
  | "same-origin"
  | "same-site"
  | "cross-origin";

export type RequestMode = "cors" | "no-cors" | "same-origin" | "navigate";

export interface ResponsePolicyContext {
  coep?: CrossOriginEmbedderPolicy | string | null;
  crossOriginEmbedderPolicy?: CrossOriginEmbedderPolicy | string | null;
  embedderPolicy?: CrossOriginEmbedderPolicy | string | null;

  corp?: CrossOriginResourcePolicy | string | null;
  crossOriginResourcePolicy?: CrossOriginResourcePolicy | string | null;
  resourcePolicy?: CrossOriginResourcePolicy | string | null;

  crossOrigin?: boolean;
  isCrossOrigin?: boolean;
  sameOrigin?: boolean;
  sameSite?: boolean;
  isSameSite?: boolean;

  mode?: RequestMode | string | null;
  requestMode?: RequestMode | string | null;
  noCors?: boolean;
  isNoCors?: boolean;

  response?: Response | null;
}

export type PolicyAction = "allow" | "opaque" | "block";

export interface ResponsePolicyDecision {
  action: PolicyAction;
  allowed: boolean;
  blocked: boolean;
  opaque: boolean;
  credentialsStripped: boolean;
  response: Response | null;
  reason: string;
}

function normalizeToken(value: unknown): string | null {
  if (value === null || value === undefined) return null;

  const token = String(value).trim().toLowerCase().replaceAll("_", "-");
  if (token === "" || token === "none" || token === "null") return null;

  return token;
}

function normalizeCoep(value: unknown): CrossOriginEmbedderPolicy {
  const token = normalizeToken(value);

  if (token === "credentialless") return "credentialless";
  if (token === "require-corp" || token === "requirecorp") {
    return "require-corp";
  }

  return "unsafe-none";
}

function normalizeCorp(value: unknown): CrossOriginResourcePolicy | null {
  const token = normalizeToken(value);

  if (token === "same-origin" || token === "sameorigin") {
    return "same-origin";
  }
  if (token === "same-site" || token === "samesite") return "same-site";
  if (token === "cross-origin" || token === "crossorigin") {
    return "cross-origin";
  }

  return null;
}

function normalizeMode(value: unknown): RequestMode {
  const token = normalizeToken(value);

  if (token === "cors") return "cors";
  if (token === "same-origin" || token === "sameorigin") return "same-origin";
  if (token === "navigate") return "navigate";

  return "no-cors";
}

function firstDefined(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined && value !== null) return value;
  }

  return undefined;
}

function firstPresent(...values: unknown[]): unknown {
  for (const value of values) {
    if (value !== undefined) return value;
  }

  return undefined;
}

function contextFrom(value: ResponsePolicyContext): ResponsePolicyContext {
  return {
    coep: firstDefined(
      value.coep,
      value.crossOriginEmbedderPolicy,
      value.embedderPolicy,
    ) as ResponsePolicyContext["coep"],
    corp: firstPresent(
      value.corp,
      value.crossOriginResourcePolicy,
      value.resourcePolicy,
    ) as ResponsePolicyContext["corp"],
    crossOrigin: firstDefined(value.crossOrigin, value.isCrossOrigin) as
      | boolean
      | undefined,
    sameOrigin: value.sameOrigin,
    sameSite: firstDefined(value.sameSite, value.isSameSite) as
      | boolean
      | undefined,
    mode: firstDefined(value.mode, value.requestMode) as
      | RequestMode
      | string
      | null
      | undefined,
    noCors: firstDefined(value.noCors, value.isNoCors) as
      | boolean
      | undefined,
    response: value.response,
  };
}

function responseCorp(response: Response | null): CrossOriginResourcePolicy | null {
  if (response === null) return null;

  try {
    return normalizeCorp(
      response.headers.get("Cross-Origin-Resource-Policy"),
    );
  } catch {
    return null;
  }
}

function normalizeArguments(args: unknown[]): ResponsePolicyContext {
  if (args.length === 0) return {};

  const [first, ...rest] = args;

  if (first instanceof Response) {
    return {
      ...contextFrom((rest[0] ?? {}) as ResponsePolicyContext),
      response: first,
    };
  }

  if (typeof first === "object" && first !== null) {
    return contextFrom(first as ResponsePolicyContext);
  }

  return {
    coep: first as string,
    corp: (rest[0] ?? null) as string | null,
    crossOrigin: rest[1] as boolean | undefined,
    mode: (rest[2] ?? "no-cors") as string,
  };
}

function createOpaqueResponse(): Response {
  const response = Response.error();

  Object.defineProperties(response, {
    type: { value: "opaque", configurable: true },
    status: { value: 0, configurable: true },
    statusText: { value: "", configurable: true },
    ok: { value: false, configurable: true },
    headers: { value: new Headers(), configurable: true },
    url: { value: "", configurable: true },
    redirected: { value: false, configurable: true },
  });

  return response;
}

function decision(
  action: PolicyAction,
  response: Response | null,
  reason: string,
): ResponsePolicyDecision {
  const opaque = action === "opaque";

  return {
    action,
    allowed: action === "allow",
    blocked: action === "block",
    opaque,
    credentialsStripped: opaque,
    response:
      action === "allow" ? response : opaque ? createOpaqueResponse() : null,
    reason,
  };
}

function corpAllows(
  corp: CrossOriginResourcePolicy | null,
  crossOrigin: boolean,
  sameSite: boolean,
): boolean {
  if (!crossOrigin) return true;

  if (corp === "cross-origin") return true;
  if (corp === "same-site") return sameSite;

  return false;
}

export function adjudicateResponse(
  context: ResponsePolicyContext,
): ResponsePolicyDecision;
export function adjudicateResponse(
  response: Response,
  context?: ResponsePolicyContext,
): ResponsePolicyDecision;
export function adjudicateResponse(
  coep: CrossOriginEmbedderPolicy,
  corp?: CrossOriginResourcePolicy | null,
  crossOrigin?: boolean,
  mode?: RequestMode,
): ResponsePolicyDecision;
export function adjudicateResponse(
  ...args: unknown[]
): ResponsePolicyDecision {
  const context = normalizeArguments(args);

  const coep = normalizeCoep(
    firstDefined(
      context.coep,
      context.crossOriginEmbedderPolicy,
      context.embedderPolicy,
    ),
  );
  const corp =
    context.corp === undefined && context.crossOriginResourcePolicy === undefined
      ? responseCorp(context.response ?? null)
      : normalizeCorp(
          firstPresent(
            context.corp,
            context.crossOriginResourcePolicy,
            context.resourcePolicy,
          ),
        );
  const explicitMode = firstPresent(context.mode, context.requestMode);
  const noCors = firstDefined(context.noCors, context.isNoCors);
  const mode = normalizeMode(
    explicitMode !== undefined
      ? explicitMode
      : typeof noCors === "boolean"
        ? noCors
          ? "no-cors"
          : "cors"
        : undefined,
  );
  const sameOrigin = context.sameOrigin === true;
  const crossOrigin =
    typeof context.crossOrigin === "boolean"
      ? context.crossOrigin
      : typeof context.isCrossOrigin === "boolean"
        ? context.isCrossOrigin
        : !sameOrigin;
  const sameSite =
    typeof context.sameSite === "boolean"
      ? context.sameSite
      : typeof context.isSameSite === "boolean"
        ? context.isSameSite
        : false;
  const response = context.response ?? null;

  if (!crossOrigin) {
    return decision("allow", response, "same-origin response");
  }

  if (mode !== "no-cors") {
    return decision("allow", response, "CORS response");
  }

  if (coep === "unsafe-none") {
    return decision("allow", response, "COEP is unsafe-none");
  }

  if (corpAllows(corp, crossOrigin, sameSite)) {
    return decision("allow", response, "CORP permits the response");
  }

  if (coep === "require-corp") {
    return decision(
      "block",
      null,
      "COEP require-corp requires CORP or a CORS response",
    );
  }

  return decision(
    "opaque",
    null,
    "COEP credentialless makes a cross-origin no-cors response opaque",
  );
}

export function evaluatePolicy(
  ...args: unknown[]
): ResponsePolicyDecision {
  return (adjudicateResponse as (...values: unknown[]) => ResponsePolicyDecision)(
    ...args,
  );
}

export function policyAction(
  ...args: unknown[]
): PolicyAction {
  return evaluatePolicy(...args).action;
}

export function enforceResponsePolicy(
  ...args: unknown[]
): Response | null {
  const result = evaluatePolicy(...args);

  if (result.blocked) {
    throw new COEPPolicyError(result.reason, result);
  }

  return result.response;
}

export class COEPPolicyError extends Error {
  readonly decision: ResponsePolicyDecision;

  constructor(message: string, decision: ResponsePolicyDecision) {
    super(message);
    this.name = "COEPPolicyError";
    this.decision = decision;
  }
}

export const decideResponse = adjudicateResponse;
export const evaluateResponse = adjudicateResponse;
export const resolveResponsePolicy = adjudicateResponse;
export const applyResponsePolicy = adjudicateResponse;
export const evaluateResponsePolicy = adjudicateResponse;
export const decide = adjudicateResponse;
export const adjudicate = adjudicateResponse;

export function shouldBlockResponse(...args: unknown[]): boolean {
  return evaluatePolicy(...args).blocked;
}

export function shouldMakeOpaque(...args: unknown[]): boolean {
  return evaluatePolicy(...args).opaque;
}

export { createOpaqueResponse, responseCorp as readResponseCorp };

export default {
  adjudicateResponse,
  evaluatePolicy,
  policyAction,
  enforceResponsePolicy,
  createOpaqueResponse,
  COEPPolicyError,
};
