export const PROTECTED_PREFIX = "/private/";

const PROTECTED_DIRECTORY = PROTECTED_PREFIX.slice(0, -1);

export type PathRejectionReason =
  | "invalid-input"
  | "invalid-unicode"
  | "protected-prefix"
  | "separator-mismatch"
  | "dot-segment";

export interface ResourcePathDecision {
  readonly allowed: boolean;
  readonly reason: PathRejectionReason | null;
  readonly nfc: string | null;
  readonly nfkc: string | null;
}

export class ResourcePathRejectedError extends Error {
  readonly reason: PathRejectionReason;

  constructor(reason: PathRejectionReason) {
    super(`Resource path rejected: ${reason}`);
    this.name = "ResourcePathRejectedError";
    this.reason = reason;
  }
}

function isWellFormedUnicode(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);

    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) {
        return false;
      }
      index += 1;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      return false;
    }
  }

  return true;
}

function separatorRuns(value: string): number[] {
  const runs: number[] = [];
  let currentRun = 0;

  for (const character of value) {
    if (character === "/") {
      currentRun += 1;
    } else if (currentRun > 0) {
      runs.push(currentRun);
      currentRun = 0;
    }
  }

  if (currentRun > 0) {
    runs.push(currentRun);
  }

  return runs;
}

function haveMatchingSeparators(nfc: string, nfkc: string): boolean {
  const nfcRuns = separatorRuns(nfc);
  const nfkcRuns = separatorRuns(nfkc);

  return (
    nfcRuns.length === nfkcRuns.length &&
    nfcRuns.every((run, index) => run === nfkcRuns[index])
  );
}

function hasProtectedPrefix(value: string): boolean {
  return value.startsWith(PROTECTED_PREFIX) || value === PROTECTED_DIRECTORY;
}

function hasDotSegment(value: string): boolean {
  return value
    .split("/")
    .some((segment) => segment === "." || segment === "..");
}

function rejected(
  reason: PathRejectionReason,
  nfc: string | null = null,
  nfkc: string | null = null,
): ResourcePathDecision {
  return {
    allowed: false,
    reason,
    nfc,
    nfkc,
  };
}

export function inspectResourcePath(path: string): ResourcePathDecision {
  if (
    typeof path !== "string" ||
    path.length === 0 ||
    path.includes("\0")
  ) {
    return rejected("invalid-input");
  }

  if (!isWellFormedUnicode(path)) {
    return rejected("invalid-unicode");
  }

  const nfc = path.normalize("NFC");
  const nfkc = path.normalize("NFKC");

  if (hasProtectedPrefix(nfc) || hasProtectedPrefix(nfkc)) {
    return rejected("protected-prefix", nfc, nfkc);
  }

  if (!haveMatchingSeparators(nfc, nfkc)) {
    return rejected("separator-mismatch", nfc, nfkc);
  }

  if (hasDotSegment(nfc) || hasDotSegment(nfkc)) {
    return rejected("dot-segment", nfc, nfkc);
  }

  return {
    allowed: true,
    reason: null,
    nfc,
    nfkc,
  };
}

export function isResourcePathAllowed(path: string): boolean {
  return inspectResourcePath(path).allowed;
}

export function assertResourcePathAllowed(path: string): string {
  const decision = inspectResourcePath(path);

  if (!decision.allowed) {
    throw new ResourcePathRejectedError(
      decision.reason ?? "invalid-input",
    );
  }

  return path;
}

export default isResourcePathAllowed;
