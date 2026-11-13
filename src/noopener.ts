const BLANK_TARGETS = new Set(["_blank", "_new"]);
const SELF_TARGETS = new Set(["", "_self", "_parent", "_top"]);

function normalizeTarget(target: string | undefined): string {
  return (target ?? "").trim().toLowerCase();
}

export function isBlankTarget(target: string | undefined): boolean {
  return BLANK_TARGETS.has(normalizeTarget(target));
}

export function isNamedTarget(target: string | undefined): boolean {
  const normalized = normalizeTarget(target);
  return !SELF_TARGETS.has(normalized) && !BLANK_TARGETS.has(normalized);
}

function tokenName(token: string): string {
  const eq = token.indexOf("=");
  return (eq === -1 ? token : token.slice(0, eq)).trim().toLowerCase();
}

export function hasNoopener(value: string | undefined): boolean {
  return (value ?? "")
    .split(/[\s,]+/)
    .filter(Boolean)
    .some((token) => tokenName(token) === "noopener");
}

export interface Decision {
  value: string;
  changed: boolean;
  openerKept: boolean;
}

export interface LinkDecisionInput {
  target?: string;
  rel?: string;
}

export function decideLinkRel(input: LinkDecisionInput): Decision {
  const rel = input.rel ?? "";
  if (hasNoopener(rel)) {
    return { value: rel, changed: false, openerKept: false };
  }
  if (!isBlankTarget(input.target)) {
    return { value: rel, changed: false, openerKept: false };
  }
  const next = rel.trim() ? `${rel.trim()} noopener` : "noopener";
  return { value: next, changed: true, openerKept: false };
}

export interface WindowOpenInput {
  target?: string;
  features?: string;
  keepOpener?: boolean;
}

export interface WindowOpenDecision extends Decision {
  target: string;
}

export function decideWindowOpen(input: WindowOpenInput = {}): WindowOpenDecision {
  const target = (input.target ?? "").trim() || "_self";
  const features = input.features ?? "";
  if (hasNoopener(features)) {
    return { target, value: features, changed: false, openerKept: false };
  }
  const named = isNamedTarget(target);
  if (named && input.keepOpener === true) {
    return { target, value: features, changed: false, openerKept: true };
  }
  if (!isBlankTarget(target) && !named) {
    return { target, value: features, changed: false, openerKept: false };
  }
  const trimmed = features.replace(/[,;\s]+$/, "");
  const next = trimmed ? `${trimmed},noopener` : "noopener";
  return { target, value: next, changed: true, openerKept: false };
}
