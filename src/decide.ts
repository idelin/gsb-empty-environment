import { digestValue } from "./canonical.ts";
import {
  createConfusableProgram,
  skeletonDomain,
  type ConfusableProgram,
} from "./confusables.ts";
import { assertScriptTable, scriptPolicyHolds } from "./scripts.ts";
import {
  effectiveSuffix,
  type SuffixResolution,
} from "./suffix.ts";
import {
  createHmacAliasVerifier,
  provenAliasDomains,
} from "./aliases.ts";
import { searchCandidates, type CandidateSearchResult } from "./automaton.ts";
import { tableDigestsFor } from "./tables.ts";
import type {
  AllowedRange,
  CandidateProof,
  DisplayDecision,
  DisplayRequest,
  DomainInput,
  LabelDecision,
  LabelInput,
  ResolvedLabel,
  ScriptTable,
  SuffixTable,
  VersionAttempt,
} from "./types.ts";
import {
  assertWellFormedUtf16,
  codePoints,
  hasNonAscii,
  isBoundaryInsideSurrogatePair,
  normalizeLabelKey,
  scalarSpans,
  splitDomainText,
  toAsciiLabel,
} from "./unicode.ts";

export interface PreparedRequest {
  readonly labels: readonly ResolvedLabel[];
  readonly protectedLabels: readonly string[];
  readonly versionOrder: readonly string[];
  readonly baseOffset: number;
  readonly sourceText: string | undefined;
  readonly now: number;
}

export interface VersionEvaluation {
  readonly attempt: VersionAttempt;
  readonly suffix: SuffixResolution;
  readonly program: ConfusableProgram;
  readonly scriptTable: ScriptTable;
  readonly suffixTable: SuffixTable;
  readonly scriptHolds: readonly boolean[];
  readonly proof: CandidateProof;
  readonly provenDomains: readonly string[];
}

/**
 * A candidate is an "allowed domain" when it sits under the selected suffix
 * and every label satisfies that suffix's script policy.
 */
export function candidateAllowedByScriptPolicy(
  domain: string,
  scriptTable: ScriptTable,
  suffix: SuffixResolution,
): boolean {
  const labels = splitDomainText(domain);
  if (labels.length === 0) return false;
  const keyLabels = labels.map(normalizeLabelKey);
  const suffixLabels = suffix.suffixLabels;
  if (suffixLabels.length > keyLabels.length) return false;
  const offset = keyLabels.length - suffixLabels.length;
  for (let i = 0; i < suffixLabels.length; i++) {
    if (keyLabels[offset + i] !== suffixLabels[i]) return false;
  }
  return labels.every((label) =>
    scriptPolicyHolds(scriptTable, suffix.policy, codePoints(label)),
  );
}

function findLabelInSource(
  sourceText: string,
  text: string,
  from: number,
): number {
  let search = Math.max(0, from);
  for (;;) {
    const found = sourceText.indexOf(text, search);
    if (found < 0) break;
    const beforeOk = found === 0 || sourceText[found - 1] === ".";
    const after = found + text.length;
    const afterOk = after === sourceText.length || sourceText[after] === ".";
    if (beforeOk && afterOk) return found;
    search = found + 1;
  }
  throw new Error(
    `label '${text}' is not a whole dot-separated label in sourceText`,
  );
}

/**
 * Resolve labels to the host's UTF-16 coordinate system.  Spans are absolute:
 * `baseOffset` is added to the offsets found in `sourceText`.  Boundaries are
 * rejected when they fall inside a surrogate pair.
 */
export function resolveLabels(
  input: readonly LabelInput[],
  sourceText?: string,
  baseOffset = 0,
): ResolvedLabel[] {
  if (!Array.isArray(input) || input.length === 0) {
    throw new TypeError("labels must be a non-empty array");
  }
  if (!Number.isInteger(baseOffset) || baseOffset < 0) {
    throw new TypeError("baseOffset must be a non-negative integer");
  }
  if (sourceText !== undefined) assertWellFormedUtf16(sourceText);

  let cursorLocal = 0;
  let previousEnd = -1;
  return input.map((item, index) => {
    const text = typeof item === "string" ? item : item.text;
    if (typeof text !== "string" || text.length === 0) {
      throw new TypeError(`label ${index}: text must be a non-empty string`);
    }
    if (text.includes(".")) {
      throw new TypeError(`label ${index}: a label must not contain '.'`);
    }
    assertWellFormedUtf16(text, `label ${index}`);

    let startLocal: number | undefined;
    let endLocal: number | undefined;
    if (typeof item === "object") {
      if (item.start !== undefined) startLocal = item.start - baseOffset;
      if (item.end !== undefined) endLocal = item.end - baseOffset;
    }

    if (startLocal === undefined) {
      startLocal =
        sourceText !== undefined
          ? findLabelInSource(sourceText, text, cursorLocal)
          : cursorLocal;
    }
    if (endLocal === undefined) endLocal = startLocal + text.length;

    if (
      !Number.isInteger(startLocal) ||
      !Number.isInteger(endLocal) ||
      startLocal < 0 ||
      endLocal < startLocal ||
      endLocal !== startLocal + text.length ||
      startLocal < previousEnd
    ) {
      throw new TypeError(`label ${index}: invalid UTF-16 span`);
    }

    if (sourceText !== undefined) {
      if (endLocal > sourceText.length) {
        throw new TypeError(`label ${index}: span exceeds sourceText`);
      }
      if (sourceText.slice(startLocal, endLocal) !== text) {
        throw new TypeError(
          `label ${index}: sourceText span does not contain the label text`,
        );
      }
      if (
        isBoundaryInsideSurrogatePair(sourceText, startLocal) ||
        isBoundaryInsideSurrogatePair(sourceText, endLocal)
      ) {
        throw new TypeError(
          `label ${index}: span boundary splits a surrogate pair`,
        );
      }
    }

    cursorLocal = endLocal + 1;
    previousEnd = endLocal;
    return {
      index,
      text,
      start: baseOffset + startLocal,
      end: baseOffset + endLocal,
      scalars: codePoints(text),
    };
  });
}

function protectedLabelTexts(
  domain: DomainInput | undefined,
  labels: readonly ResolvedLabel[],
): string[] {
  let values: readonly string[];
  if (domain === undefined) {
    values = labels.map((label) => label.text);
  } else if (typeof domain === "string") {
    values = splitDomainText(domain);
  } else {
    if (!Array.isArray(domain.labels) || domain.labels.length === 0) {
      throw new TypeError("protectedDomain.labels must be non-empty");
    }
    values = domain.labels;
  }
  if (values.length === 0) {
    throw new TypeError("protected domain must contain at least one label");
  }
  return values.map((label) => {
    if (typeof label !== "string" || label.length === 0) {
      throw new TypeError("protected domain labels must be non-empty strings");
    }
    if (label.includes(".")) {
      throw new TypeError("protected domain labels must not contain '.'");
    }
    assertWellFormedUtf16(label, "protected domain label");
    return normalizeLabelKey(label);
  });
}

export function prepareRequest(request: DisplayRequest): PreparedRequest {
  if (!request || !request.tables) {
    throw new TypeError("display request needs tables");
  }
  if (
    !request.tables.confusables ||
    !request.tables.scripts ||
    !request.tables.suffixes
  ) {
    throw new TypeError("display request tables need all three table maps");
  }
  if (typeof request.brand !== "string" || request.brand.length === 0) {
    throw new TypeError("display request needs a brand identity");
  }
  if (typeof request.suffixVersion !== "string") {
    throw new TypeError("display request needs a suffixVersion");
  }
  const baseOffset = request.baseOffset ?? 0;
  const now = request.now ?? Date.now();
  if (!Number.isFinite(now)) {
    throw new TypeError("now must be a finite epoch-millisecond number");
  }
  const labels = resolveLabels(request.labels, request.sourceText, baseOffset);
  const protectedLabels = protectedLabelTexts(request.protectedDomain, labels);
  const versionOrder =
    request.versionOrder ?? Object.keys(request.tables.confusables);
  if (versionOrder.length === 0) {
    throw new TypeError("display request needs at least one table version");
  }
  for (const version of versionOrder) {
    if (!request.tables.confusables[version]) {
      throw new Error(`missing confusable table '${version}'`);
    }
    if (!request.tables.scripts[version]) {
      throw new Error(`missing script table '${version}'`);
    }
    const suffixVersion =
      request.suffixVersionByTableVersion?.[version] ?? request.suffixVersion;
    if (!request.tables.suffixes[suffixVersion]) {
      throw new Error(`missing suffix table '${suffixVersion}'`);
    }
  }
  return {
    labels,
    protectedLabels,
    versionOrder,
    baseOffset,
    sourceText: request.sourceText,
    now,
  };
}

function vacuousProof(
  targetSkeleton: readonly number[],
  provenDomains: readonly string[],
): CandidateProof {
  return {
    ok: true,
    truncated: false,
    targetSkeleton,
    provenDomains,
    acceptedSameSkeleton: 0,
    provenSameSkeleton: 0,
    witness: null,
    exploredStates: 0,
    reason: "no-automaton",
  };
}

function proofFromSearch(
  search: CandidateSearchResult,
): CandidateProof {
  return {
    ok: search.ok,
    truncated: search.truncated,
    targetSkeleton: search.targetSkeleton,
    provenDomains: search.provenDomains,
    acceptedSameSkeleton: search.acceptedSameSkeleton,
    provenSameSkeleton: search.provenSameSkeleton,
    witness: search.witness,
    exploredStates: search.exploredStates,
    reason: search.reason,
  };
}

export function evaluateVersionAttempt(
  request: DisplayRequest,
  prepared: PreparedRequest,
  version: string,
): VersionEvaluation {
  const confusableTable = request.tables.confusables[version];
  const scriptTable = request.tables.scripts[version];
  if (!confusableTable) throw new Error(`missing confusable table '${version}'`);
  if (!scriptTable) throw new Error(`missing script table '${version}'`);
  const suffixVersion =
    request.suffixVersionByTableVersion?.[version] ?? request.suffixVersion;
  const suffixTable = request.tables.suffixes[suffixVersion];
  if (!suffixTable) throw new Error(`missing suffix table '${suffixVersion}'`);
  assertScriptTable(scriptTable);

  const program = createConfusableProgram(confusableTable);
  const suffix = effectiveSuffix(
    prepared.protectedLabels,
    suffixTable,
    request.defaultScriptPolicy,
  );
  const targetSkeleton = skeletonDomain(program, prepared.protectedLabels);

  const verifier =
    request.verifier ??
    (request.keyring
      ? createHmacAliasVerifier(request.keyring, {
          keyEncoding: request.aliasKeyEncoding ?? "utf8",
        })
      : undefined);
  const proven = provenAliasDomains(request.aliases ?? [], {
    brand: request.brand,
    suffix: suffix.suffix,
    tableVersion: version,
    suffixVersion,
    now: prepared.now,
    ...(verifier ? { verifier } : {}),
  });

  const proof = request.automaton
    ? proofFromSearch(
        searchCandidates(request.automaton, program, targetSkeleton, {
          maxScalars: request.maxCandidateScalars ?? 64,
          maxStates: request.maxCandidateStates ?? 1_000_000,
          provenDomains: proven.domains,
          ...((request.filterCandidatesByScriptPolicy ?? true)
            ? {
                isAllowedDomain: (domain: string) =>
                  candidateAllowedByScriptPolicy(
                    domain,
                    scriptTable,
                    suffix,
                  ),
              }
            : {}),
        }),
      )
    : vacuousProof(targetSkeleton, [...proven.domains].sort());

  const scriptHolds = prepared.labels.map((label) =>
    scriptPolicyHolds(scriptTable, suffix.policy, label.scalars),
  );
  const tableDigests = tableDigestsFor(
    request.tables,
    version,
    suffixVersion,
  );
  const attempt: VersionAttempt = {
    version,
    suffixVersion,
    proof,
    scriptPolicyHolds: scriptHolds,
    ok: proof.ok,
    reason: proof.ok ? "proof" : proof.reason,
    tableDigests,
  };
  return {
    attempt,
    suffix,
    program,
    scriptTable,
    suffixTable,
    scriptHolds,
    proof,
    provenDomains: [...proven.domains].sort(),
  };
}

export function computeAllowedRange(
  decisions: readonly LabelDecision[],
  version: string,
  suffixVersion: string,
): AllowedRange {
  const unicodeLabels = decisions
    .filter((decision) => decision.mode === "unicode")
    .map((decision) => decision.index);
  const asciiLabels = decisions
    .filter((decision) => decision.mode === "ascii")
    .map((decision) => decision.index);
  const digest = digestValue({
    version,
    suffixVersion,
    labels: decisions.map((decision) => ({
      index: decision.index,
      mode: decision.mode,
      displayText: decision.displayText,
      span: decision.span,
      reason: decision.reason,
    })),
  });
  return { unicodeLabels, asciiLabels, digest };
}

/**
 * Produce per-label display decisions.  Versions are tried newest first; the
 * first version whose domain-level alias proof succeeds is selected.  A label
 * that fails the selected version's script policy falls back to its A-label
 * independently of the other labels.
 */
export function decideDisplay(request: DisplayRequest): DisplayDecision {
  const prepared = prepareRequest(request);
  const evaluations = prepared.versionOrder.map((version) =>
    evaluateVersionAttempt(request, prepared, version),
  );
  const first = 0;
  const found = request.versionFallback
    ? evaluations.findIndex((evaluation) => evaluation.attempt.ok)
    : evaluations[first]?.attempt.ok
      ? first
      : -1;
  const chosenIndex = found >= 0 ? found : first;
  const chosen = evaluations[chosenIndex];
  const proofOk = chosen.attempt.proof.ok;

  const decisions: LabelDecision[] = prepared.labels.map((label, index) => {
    const asciiText = toAsciiLabel(label.text);
    const nonAscii = hasNonAscii(label.text);
    const scriptOk = chosen.scriptHolds[index];
    let mode: LabelDecision["mode"];
    let displayText: string;
    let reason: LabelDecision["reason"];
    if (!nonAscii) {
      mode = "ascii";
      displayText = asciiText;
      reason = "ascii-input";
    } else if (proofOk && scriptOk) {
      mode = "unicode";
      displayText = label.text;
      reason = "proved";
    } else if (proofOk) {
      mode = "ascii";
      displayText = asciiText;
      reason = "script-policy";
    } else {
      mode = "ascii";
      displayText = asciiText;
      reason = chosen.attempt.proof.truncated
        ? "search-truncated"
        : "no-proof";
    }
    return {
      index,
      label: label.text,
      mode,
      displayText,
      asciiText,
      version: chosen.attempt.version,
      span: { start: label.start, end: label.end },
      scalars: scalarSpans(label.text, label.start),
      scriptPolicyHolds: scriptOk,
      reason,
    };
  });

  return {
    labels: decisions,
    text: decisions.map((decision) => decision.displayText).join("."),
    version: chosen.attempt.version,
    chosenAttempt: chosenIndex,
    now: prepared.now,
    attempts: evaluations.map((evaluation) => evaluation.attempt),
    suffix: {
      value: chosen.suffix.suffix,
      version: chosen.suffix.version,
      labels: chosen.suffix.suffixLabels,
      policy: chosen.suffix.policy,
    },
    tableDigests: chosen.attempt.tableDigests,
    allowedRange: computeAllowedRange(
      decisions,
      chosen.attempt.version,
      chosen.attempt.suffixVersion,
    ),
    proof: chosen.attempt.proof,
  };
}

/**
 * Single-version decision, useful when a bookmark is pinned to the table
 * version under which it was saved.
 */
export function decideForVersion(
  request: DisplayRequest,
  version: string,
): DisplayDecision {
  return decideDisplay({
    ...request,
    versionOrder: [version],
  });
}

/**
 * Evaluate each requested version independently.  A host can use the old
 * version's decision for a bookmark and the new version's decision for a
 * freshly seen domain without conflating the two.
 */
export function decideAllVersions(
  request: DisplayRequest,
): readonly DisplayDecision[] {
  const versions =
    request.versionOrder ?? Object.keys(request.tables.confusables);
  if (versions.length === 0) {
    throw new TypeError("display request needs at least one table version");
  }
  return versions.map((version) => decideForVersion(request, version));
}
