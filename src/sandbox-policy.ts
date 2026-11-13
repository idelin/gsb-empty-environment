export const SANDBOX_FLAG = {
  scripts: "allow-scripts",
  sameOrigin: "allow-same-origin",
} as const;

export type SandboxFlag = (typeof SANDBOX_FLAG)[keyof typeof SANDBOX_FLAG];

export interface SandboxFlagSelection {
  readonly allowScripts: boolean;
  readonly allowSameOrigin: boolean;
}

export interface SandboxApproval {
  readonly allowed: true;
  readonly flags: SandboxFlagSelection;
  readonly sandboxAttribute: string;
  readonly origin: "opaque" | "inherited";
  readonly executesScripts: boolean;
}

export interface SandboxRejection {
  readonly allowed: false;
  readonly flags: SandboxFlagSelection;
  readonly reason: "conflicting-origin-and-scripts";
  readonly message: string;
}

export type SandboxVerdict = SandboxApproval | SandboxRejection;
export type SandboxFlagsInput = SandboxFlagSelection | readonly SandboxFlag[] | string;

export function isSandboxFlag(value: string): value is SandboxFlag {
  return value === SANDBOX_FLAG.scripts || value === SANDBOX_FLAG.sameOrigin;
}

export function normalizeSandboxFlags(input: SandboxFlagsInput): SandboxFlagSelection {
  if (typeof input === "string") {
    return normalizeSandboxTokens(input.split(/\s+/));
  }

  if (Array.isArray(input)) {
    return normalizeSandboxTokens(input);
  }

  return {
    allowScripts: input.allowScripts === true,
    allowSameOrigin: input.allowSameOrigin === true,
  };
}

function normalizeSandboxTokens(tokens: readonly string[]): SandboxFlagSelection {
  let allowScripts = false;
  let allowSameOrigin = false;

  for (const token of tokens) {
    if (token.length === 0) {
      continue;
    }
    if (!isSandboxFlag(token)) {
      throw new TypeError(`Unsupported sandbox flag: ${token}`);
    }
    if (token === SANDBOX_FLAG.scripts) {
      allowScripts = true;
    } else {
      allowSameOrigin = true;
    }
  }

  return { allowScripts, allowSameOrigin };
}

function sandboxAttributeFor(flags: SandboxFlagSelection): string {
  const tokens: SandboxFlag[] = [];
  if (flags.allowScripts) {
    tokens.push(SANDBOX_FLAG.scripts);
  }
  if (flags.allowSameOrigin) {
    tokens.push(SANDBOX_FLAG.sameOrigin);
  }
  return tokens.join(" ");
}

export function adjudicateSandboxFlags(input: SandboxFlagsInput): SandboxVerdict {
  const flags = normalizeSandboxFlags(input);

  if (flags.allowScripts && flags.allowSameOrigin) {
    return {
      allowed: false,
      flags,
      reason: "conflicting-origin-and-scripts",
      message:
        "srcdoc must not combine allow-scripts with allow-same-origin: " +
        "the combination inherits the parent origin and executes scripts",
    };
  }

  const origin = flags.allowSameOrigin ? "inherited" : "opaque";
  const approval: SandboxApproval = {
    allowed: true,
    flags,
    sandboxAttribute: sandboxAttributeFor(flags),
    origin,
    executesScripts: flags.allowScripts,
  };

  if (approval.executesScripts && approval.origin !== "opaque") {
    throw new Error("invariant violated: script execution requires an opaque origin");
  }

  return approval;
}

export class SandboxPolicyError extends Error {
  readonly verdict: SandboxRejection;

  constructor(verdict: SandboxRejection) {
    super(verdict.message);
    this.name = "SandboxPolicyError";
    this.verdict = verdict;
  }
}

export function requireSandboxApproval(input: SandboxFlagsInput): SandboxApproval {
  const verdict = adjudicateSandboxFlags(input);
  if (!verdict.allowed) {
    throw new SandboxPolicyError(verdict);
  }
  return verdict;
}
