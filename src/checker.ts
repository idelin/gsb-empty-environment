import { digestValue } from "./canonical.ts";
import {
  createConfusableProgram,
  skeletonDomain,
  transcribe,
} from "./confusables.ts";
import {
  candidateAllowedByScriptPolicy,
  computeAllowedRange,
  evaluateVersionAttempt,
  prepareRequest,
  type PreparedRequest,
  type VersionEvaluation,
} from "./decide.ts";
import {
  createHmacAliasVerifier,
  provenAliasDomains,
} from "./aliases.ts";
import { enumerateAcceptedWords } from "./automaton.ts";
import { scriptPolicyHolds } from "./scripts.ts";
import { effectiveSuffix, type SuffixResolution } from "./suffix.ts";
import { tableDigestsFor, summarizeTables } from "./tables.ts";
import type {
  DisplayDecision,
  DisplayRequest,
  Scalar,
  TableSummary,
  VerificationReport,
  VersionedTables,
} from "./types.ts";
import {
  isBoundaryInsideSurrogatePair,
  isUnicodeScalar,
  normalizeDomainKey,
  toAsciiLabel,
} from "./unicode.ts";

export interface VerifyOptions {
  /** Upper bound on words the independent brute-force oracle may inspect. */
  readonly exhaustiveCandidateLimit?: number;
  /** Recompute every version attempt, not only the chosen one. */
  readonly verifyAllAttempts?: boolean;
}

function sameScalars(a: readonly Scalar[], b: readonly Scalar[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function sameStrings(
  a: readonly string[],
  b: readonly string[],
): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function hostFor(
  request: DisplayRequest,
  prepared: PreparedRequest,
): { text: string; offset: number } {
  if (request.sourceText !== undefined) {
    return { text: request.sourceText, offset: prepared.baseOffset };
  }
  return {
    text: prepared.labels.map((label) => label.text).join("."),
    offset: prepared.baseOffset,
  };
}

function verifyLabelSpans(
  decision: DisplayDecision,
  request: DisplayRequest,
  prepared: PreparedRequest,
  errors: string[],
): number {
  const host = hostFor(request, prepared);
  let checked = 0;
  if (decision.labels.length !== prepared.labels.length) {
    errors.push(
      `label count ${decision.labels.length} does not match input ${prepared.labels.length}`,
    );
    return checked;
  }

  for (let index = 0; index < decision.labels.length; index++) {
    const label = decision.labels[index];
    const resolved = prepared.labels[index];
    checked++;
    if (label.index !== index) errors.push(`label ${index}: wrong index`);
    if (label.label !== resolved.text) {
      errors.push(`label ${index}: text does not match input`);
    }
    if (label.span.start !== resolved.start || label.span.end !== resolved.end) {
      errors.push(`label ${index}: span does not match resolved input span`);
    }
    if (label.version !== decision.version) {
      errors.push(`label ${index}: version is not the chosen version`);
    }

    const localStart = label.span.start - host.offset;
    const localEnd = label.span.end - host.offset;
    if (
      !Number.isInteger(localStart) ||
      !Number.isInteger(localEnd) ||
      localStart < 0 ||
      localEnd > host.text.length ||
      localEnd < localStart
    ) {
      errors.push(`label ${index}: span is outside the host text`);
      continue;
    }
    if (host.text.slice(localStart, localEnd) !== label.label) {
      errors.push(`label ${index}: host slice does not equal the label`);
    }
    if (
      isBoundaryInsideSurrogatePair(host.text, localStart) ||
      isBoundaryInsideSurrogatePair(host.text, localEnd)
    ) {
      errors.push(`label ${index}: span splits a surrogate pair`);
    }

    if (label.scalars.length === 0) {
      errors.push(`label ${index}: missing scalar spans`);
    } else {
      if (label.scalars[0].utf16Start !== label.span.start) {
        errors.push(`label ${index}: first scalar does not start at the label`);
      }
      if (label.scalars[label.scalars.length - 1].utf16End !== label.span.end) {
        errors.push(`label ${index}: last scalar does not end at the label`);
      }
      let reconstructed = "";
      for (let i = 0; i < label.scalars.length; i++) {
        const span = label.scalars[i];
        if (i > 0 && span.utf16Start !== label.scalars[i - 1].utf16End) {
          errors.push(`label ${index}: scalar spans are not contiguous`);
        }
        if (span.utf16End <= span.utf16Start) {
          errors.push(`label ${index}: scalar span is empty`);
        }
        if (!isUnicodeScalar(span.codePoint)) {
          errors.push(`label ${index}: scalar span is not a Unicode scalar`);
          continue;
        }
        const character = String.fromCodePoint(span.codePoint);
        if (span.utf16End - span.utf16Start !== character.length) {
          errors.push(`label ${index}: scalar span width is wrong`);
        }
        const scalarStartLocal = span.utf16Start - host.offset;
        const scalarEndLocal = span.utf16End - host.offset;
        if (
          isBoundaryInsideSurrogatePair(host.text, scalarStartLocal) ||
          isBoundaryInsideSurrogatePair(host.text, scalarEndLocal)
        ) {
          errors.push(`label ${index}: scalar span splits a surrogate pair`);
        }
        reconstructed += character;
      }
      if (reconstructed !== label.label) {
        errors.push(`label ${index}: scalar spans do not reconstruct the label`);
      }
    }

    let expectedAscii: string;
    try {
      expectedAscii = toAsciiLabel(label.label);
    } catch (error) {
      errors.push(
        `label ${index}: cannot compute A-label: ${(error as Error).message}`,
      );
      continue;
    }
    if (label.asciiText !== expectedAscii) {
      errors.push(`label ${index}: asciiText is not the A-label`);
    }
    if (label.mode === "unicode") {
      if (label.displayText !== label.label) {
        errors.push(`label ${index}: unicode mode does not show the label`);
      }
      if (label.reason !== "proved") {
        errors.push(`label ${index}: unicode mode has reason '${label.reason}'`);
      }
      if (!label.scriptPolicyHolds) {
        errors.push(`label ${index}: unicode mode with failed script policy`);
      }
      if (!decision.proof.ok) {
        errors.push(`label ${index}: unicode mode without a successful proof`);
      }
    } else if (label.mode === "ascii") {
      if (label.displayText !== label.asciiText) {
        errors.push(`label ${index}: ascii mode does not show the A-label`);
      }
    } else {
      errors.push(`label ${index}: unknown display mode`);
    }
  }

  const expectedText = decision.labels
    .map((label) => label.displayText)
    .join(".");
  if (decision.text !== expectedText) {
    errors.push("decision text does not match the label decisions");
  }
  return checked;
}

function verifyTableDigests(
  decision: DisplayDecision,
  request: DisplayRequest,
  options: VerifyOptions,
  errors: string[],
): number {
  const summary = summarizeTables(request.tables);
  let checked = 0;
  const attempts = (options.verifyAllAttempts ?? true)
    ? decision.attempts
    : decision.attempts.slice(decision.chosenAttempt, decision.chosenAttempt + 1);

  for (const attempt of attempts) {
    let expected: Readonly<Record<string, string>>;
    try {
      expected = tableDigestsFor(
        request.tables,
        attempt.version,
        attempt.suffixVersion,
      );
    } catch (error) {
      errors.push(
        `attempt ${attempt.version}: ${(error as Error).message}`,
      );
      continue;
    }
    for (const key of ["confusables", "scripts", "suffixes", "combined"]) {
      if (attempt.tableDigests[key] !== expected[key]) {
        errors.push(
          `attempt ${attempt.version}: table digest '${key}' mismatch`,
        );
      }
      checked++;
    }
    const summaryConfusable = summary.confusables[attempt.version];
    const summaryScript = summary.scripts[attempt.version];
    const summarySuffix = summary.suffixes[attempt.suffixVersion];
    if (summaryConfusable?.digest !== expected.confusables) {
      errors.push(`attempt ${attempt.version}: confusable summary mismatch`);
    }
    if (summaryScript?.digest !== expected.scripts) {
      errors.push(`attempt ${attempt.version}: script summary mismatch`);
    }
    if (summarySuffix?.digest !== expected.suffixes) {
      errors.push(`attempt ${attempt.version}: suffix summary mismatch`);
    }
  }

  const chosen = decision.attempts[decision.chosenAttempt];
  if (!chosen) {
    errors.push("chosen attempt is missing");
    return checked;
  }
  for (const key of ["confusables", "scripts", "suffixes", "combined"]) {
    if (decision.tableDigests[key] !== chosen.tableDigests[key]) {
      errors.push(`decision table digest '${key}' does not match chosen attempt`);
    }
  }
  return checked;
}

interface BruteForceResult {
  readonly ok: boolean | null;
  readonly checked: number;
  readonly truncated: boolean;
  readonly violation: string | null;
}

function bruteForceProof(
  request: DisplayRequest,
  prepared: PreparedRequest,
  version: string,
  suffixVersion: string,
  target: readonly Scalar[],
  limit: number,
): BruteForceResult {
  if (!request.automaton) {
    return { ok: true, checked: 0, truncated: false, violation: null };
  }
  const confusableTable = request.tables.confusables[version];
  const scriptTable = request.tables.scripts[version];
  const suffixTable = request.tables.suffixes[suffixVersion];
  if (!confusableTable || !scriptTable || !suffixTable) {
    return {
      ok: null,
      checked: 0,
      truncated: true,
      violation: "missing table",
    };
  }
  const program = createConfusableProgram(confusableTable);
  let suffix: SuffixResolution | null = null;
  try {
    suffix = effectiveSuffix(
      prepared.protectedLabels,
      suffixTable,
      request.defaultScriptPolicy,
    );
  } catch (error) {
    errors.push(
      `suffix recomputation failed: ${(error as Error).message}`,
    );
    return;
  }
  if (!suffix) return;
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

  const enumeration = enumerateAcceptedWords(
    request.automaton,
    Math.max(0, Math.min(64, request.maxCandidateScalars ?? 64)),
    limit,
  );
  if (enumeration.truncated) {
    return {
      ok: null,
      checked: enumeration.words.length,
      truncated: true,
      violation: null,
    };
  }
  let checked = 0;
  for (const word of enumeration.words) {
    const skeleton = transcribe(program, word);
    if (!sameScalars(skeleton, target)) continue;
    if (
      (request.filterCandidatesByScriptPolicy ?? true) &&
      !candidateAllowedByScriptPolicy(word, scriptTable, suffix)
    ) {
      continue;
    }
    checked++;
    if (!proven.domains.has(normalizeDomainKey(word))) {
      return {
        ok: false,
        checked,
        truncated: false,
        violation: word,
      };
    }
  }
  return { ok: true, checked, truncated: false, violation: null };
}

function verifyProof(
  decision: DisplayDecision,
  request: DisplayRequest,
  prepared: PreparedRequest,
  options: VerifyOptions,
  errors: string[],
): void {
  const chosen = decision.attempts[decision.chosenAttempt];
  if (!chosen) return;
  const checkPrepared: PreparedRequest = { ...prepared, now: decision.now };
  let evaluation: VersionEvaluation | null = null;
  try {
    evaluation = evaluateVersionAttempt(request, checkPrepared, decision.version);
  } catch (error) {
    errors.push(`proof recomputation failed: ${(error as Error).message}`);
    return;
  }
  if (!evaluation) return;
  if (chosen.ok !== evaluation.attempt.ok) {
    errors.push("chosen attempt ok flag does not match recomputation");
  }
  if (chosen.reason !== evaluation.attempt.reason) {
    errors.push("chosen attempt reason does not match recomputation");
  }
  if (
    chosen.scriptPolicyHolds.length !== evaluation.scriptHolds.length ||
    chosen.scriptPolicyHolds.some(
      (value, index) => value !== evaluation.scriptHolds[index],
    )
  ) {
    errors.push("chosen attempt script policy flags do not match recomputation");
  }
  for (const key of ["confusables", "scripts", "suffixes", "combined"]) {
    if (chosen.tableDigests[key] !== evaluation.attempt.tableDigests[key]) {
      errors.push(
        `chosen attempt digest '${key}' does not match recomputation`,
      );
    }
  }
  if (decision.proof.ok !== chosen.proof.ok) {
    errors.push("decision proof is not the chosen attempt's proof");
  }
  const expected = evaluation.proof;
  if (decision.proof.ok !== expected.ok) {
    errors.push("proof ok flag does not match recomputation");
  }
  if (decision.proof.truncated !== expected.truncated) {
    errors.push("proof truncated flag does not match recomputation");
  }
  if (!sameScalars(decision.proof.targetSkeleton, expected.targetSkeleton)) {
    errors.push("proof target skeleton does not match recomputation");
  }
  if (
    !sameStrings(
      [...decision.proof.provenDomains].sort(),
      [...expected.provenDomains].sort(),
    )
  ) {
    errors.push("proof proven-domain set does not match recomputation");
  }
  if (decision.proof.witness?.domain !== expected.witness?.domain) {
    errors.push("proof witness does not match recomputation");
  }
  if (decision.proof.ok && decision.proof.witness) {
    errors.push("successful proof still contains a witness");
  }

  if (options.verifyAllAttempts ?? true) {
    for (const attempt of decision.attempts) {
      let attemptEvaluation: VersionEvaluation | null = null;
      try {
        attemptEvaluation = evaluateVersionAttempt(
          request,
          checkPrepared,
          attempt.version,
        );
      } catch (error) {
        errors.push(
          `attempt ${attempt.version} recomputation failed: ${(error as Error).message}`,
        );
        continue;
      }
      if (!attemptEvaluation) continue;
      if (attempt.ok !== attemptEvaluation.attempt.ok) {
        errors.push(`attempt ${attempt.version}: ok flag mismatch`);
      }
      if (attempt.reason !== attemptEvaluation.attempt.reason) {
        errors.push(`attempt ${attempt.version}: reason mismatch`);
      }
      if (
        attempt.scriptPolicyHolds.length !==
          attemptEvaluation.scriptHolds.length ||
        attempt.scriptPolicyHolds.some(
          (value, index) =>
            value !== attemptEvaluation.scriptHolds[index],
        )
      ) {
        errors.push(`attempt ${attempt.version}: script policy mismatch`);
      }
      if (attempt.proof.ok !== attemptEvaluation.proof.ok) {
        errors.push(`attempt ${attempt.version}: proof ok mismatch`);
      }
      if (
        !sameScalars(
          attempt.proof.targetSkeleton,
          attemptEvaluation.proof.targetSkeleton,
        )
      ) {
        errors.push(`attempt ${attempt.version}: target skeleton mismatch`);
      }
      if (
        !sameStrings(
          [...attempt.proof.provenDomains].sort(),
          [...attemptEvaluation.proof.provenDomains].sort(),
        )
      ) {
        errors.push(`attempt ${attempt.version}: proven domains mismatch`);
      }
      if (attempt.proof.witness?.domain !== attemptEvaluation.proof.witness?.domain) {
        errors.push(`attempt ${attempt.version}: witness mismatch`);
      }
    }
  }

  const decisionTable = request.tables.confusables[decision.version];
  if (!decisionTable) {
    errors.push("chosen confusable table is missing");
    return;
  }
  const program = createConfusableProgram(decisionTable);
  const target = skeletonDomain(program, prepared.protectedLabels);
  if (!sameScalars(target, decision.proof.targetSkeleton)) {
    errors.push("target skeleton is not the protected domain skeleton");
  }

  if (decision.proof.witness) {
    const witness = decision.proof.witness;
    if (
      witness.scalars.some((scalar) => !isUnicodeScalar(scalar))
    ) {
      errors.push("witness contains a non-scalar value");
    }
    let skeleton: Scalar[];
    try {
      skeleton = transcribe(program, witness.domain);
    } catch (error) {
      errors.push(
        `witness domain cannot be transcribed: ${(error as Error).message}`,
      );
      skeleton = [];
    }
    if (!sameScalars(skeleton, decision.proof.targetSkeleton)) {
      errors.push("witness skeleton does not equal the target skeleton");
    }
    if (
      !sameScalars(
        witness.scalars,
        Array.from(witness.domain, (character) =>
          character.codePointAt(0) as number,
        ),
      )
    ) {
      errors.push("witness scalar array does not reconstruct its domain");
    }
    if (
      decision.proof.provenDomains.includes(
        normalizeDomainKey(witness.domain),
      )
    ) {
      errors.push("witness domain is actually covered by an alias");
    }
  }

  const brute = bruteForceProof(
    request,
    checkPrepared,
    decision.version,
    chosen.suffixVersion,
    decision.proof.targetSkeleton,
    Math.max(1, options.exhaustiveCandidateLimit ?? 20000),
  );
  if (decision.proof.truncated && decision.proof.ok) {
    errors.push("a truncated proof must not allow Unicode");
  }
  if (
    !brute.truncated &&
    brute.ok !== null &&
    !decision.proof.truncated
  ) {
    if (brute.ok !== decision.proof.ok) {
      errors.push(
        `independent candidate scan disagrees: expected ok=${String(brute.ok)}${
          brute.violation ? ` (witness '${brute.violation}')` : ""
        }`,
      );
    }
  }
}

function verifyAllowedRange(
  decision: DisplayDecision,
  request: DisplayRequest,
  prepared: PreparedRequest,
  errors: string[],
): void {
  if (decision.labels.length !== prepared.labels.length) return;
  const chosen = decision.attempts[decision.chosenAttempt];
  if (!chosen) return;
  const expected = computeAllowedRange(
    decision.labels,
    decision.version,
    chosen.suffixVersion,
  );
  if (
    !sameStrings(
      [...decision.allowedRange.unicodeLabels].map(String),
      [...expected.unicodeLabels].map(String),
    )
  ) {
    errors.push("allowed unicode-label range does not match label decisions");
  }
  if (
    !sameStrings(
      [...decision.allowedRange.asciiLabels].map(String),
      [...expected.asciiLabels].map(String),
    )
  ) {
    errors.push("allowed ascii-label range does not match label decisions");
  }
  if (decision.allowedRange.digest !== expected.digest) {
    errors.push("allowed-range digest mismatch");
  }

  const suffixTable = request.tables.suffixes[chosen.suffixVersion];
  const scriptTable = request.tables.scripts[decision.version];
  if (!suffixTable || !scriptTable) {
    errors.push("chosen suffix or script table is missing");
    return;
  }
  const suffix = effectiveSuffix(
    prepared.protectedLabels,
    suffixTable,
    request.defaultScriptPolicy,
  );
  if (decision.suffix.value !== suffix.suffix) {
    errors.push("resolved suffix does not match the selected suffix table");
  }
  if (decision.suffix.version !== suffix.version) {
    errors.push("resolved suffix version does not match");
  }
  if (!sameStrings(decision.suffix.labels, suffix.suffixLabels)) {
    errors.push("resolved suffix labels do not match");
  }
  if (
    digestValue(decision.suffix.policy ?? null) !==
    digestValue(suffix.policy ?? null)
  ) {
    errors.push("resolved suffix policy does not match");
  }

  decision.labels.forEach((label, index) => {
    const resolved = prepared.labels[index];
    let holds: boolean;
    try {
      holds = scriptPolicyHolds(
        scriptTable,
        suffix.policy,
        resolved.scalars,
      );
    } catch (error) {
      errors.push(
        `label ${index}: script policy recomputation failed: ${(error as Error).message}`,
      );
      holds = false;
    }
    if (label.scriptPolicyHolds !== holds) {
      errors.push(`label ${index}: script policy flag does not recompute`);
    }
    if (label.mode === "ascii") {
      const isAscii = !Array.from(label.label).some(
        (character) => (character.codePointAt(0) as number) > 0x7f,
      );
      if (!isAscii && decision.proof.ok && holds) {
        errors.push(
          `label ${index}: Unicode was provable but the decision fell back`,
        );
      }
    }
    if (label.mode === "unicode" && (!holds || !decision.proof.ok)) {
      errors.push(`label ${index}: Unicode was allowed without proof/policy`);
    }
  });
}

/**
 * Independent checker for a produced decision.  It recomputes table digests,
 * validates every UTF-16/scalar span, rechecks the proof's target and alias
 * set, and (when the automaton is small) runs a brute-force candidate scan.
 */
export function verifyDecision(
  decision: DisplayDecision,
  request: DisplayRequest,
  options: VerifyOptions = {},
): VerificationReport {
  const errors: string[] = [];
  let prepared: PreparedRequest;
  try {
    prepared = prepareRequest(request);
  } catch (error) {
    return {
      ok: false,
      errors: [`request does not prepare: ${(error as Error).message}`],
      checkedLabels: 0,
      checkedTables: 0,
    };
  }
  if (!Number.isFinite(decision.now)) {
    errors.push("decision.now is not a finite epoch-millisecond number");
  }
  if (request.now !== undefined && request.now !== decision.now) {
    errors.push("decision.now does not match request.now");
  }

  const checkedLabels = verifyLabelSpans(
    decision,
    request,
    prepared,
    errors,
  );
  const checkedTables = verifyTableDigests(
    decision,
    request,
    options,
    errors,
  );
  verifyProof(decision, request, prepared, options, errors);
  verifyAllowedRange(decision, request, prepared, errors);

  if (decision.chosenAttempt < 0 || decision.chosenAttempt >= decision.attempts.length) {
    errors.push("chosen attempt index is out of range");
  }
  if (decision.attempts.length !== prepared.versionOrder.length) {
    errors.push("attempt count does not match version order");
  }
  decision.attempts.forEach((attempt, index) => {
    if (attempt.version !== prepared.versionOrder[index]) {
      errors.push(`attempt ${index}: version does not match version order`);
    }
  });
  const firstOk = decision.attempts.findIndex((attempt) => attempt.ok);
  const expectedChosen =
    request.versionFallback && firstOk >= 0 ? firstOk : 0;
  if (decision.chosenAttempt !== expectedChosen) {
    errors.push("chosen attempt is not the first successful version");
  }
  if (
    decision.version !==
    decision.attempts[decision.chosenAttempt]?.version
  ) {
    errors.push("decision version does not match the chosen attempt");
  }
  return {
    ok: errors.length === 0,
    errors,
    checkedLabels,
    checkedTables,
  };
}

/**
 * Verify only the allowed/fallback range and its digest.  Useful as a cheap
 * host-side guard before rendering.
 */
export function checkAllowedRange(
  decision: DisplayDecision,
  request: DisplayRequest,
): VerificationReport {
  const errors: string[] = [];
  let prepared: PreparedRequest;
  try {
    prepared = prepareRequest(request);
  } catch (error) {
    return {
      ok: false,
      errors: [`request does not prepare: ${(error as Error).message}`],
      checkedLabels: 0,
      checkedTables: 0,
    };
  }
  const checkedLabels = verifyLabelSpans(
    decision,
    request,
    prepared,
    errors,
  );
  verifyAllowedRange(decision, request, prepared, errors);
  return {
    ok: errors.length === 0,
    errors,
    checkedLabels,
    checkedTables: 0,
  };
}

export function digestDecision(decision: DisplayDecision): string {
  return digestValue({
    version: decision.version,
    now: decision.now,
    text: decision.text,
    labels: decision.labels.map((label) => ({
      index: label.index,
      mode: label.mode,
      displayText: label.displayText,
      span: label.span,
      reason: label.reason,
    })),
    allowedRange: decision.allowedRange,
    tableDigests: decision.tableDigests,
    suffix: decision.suffix,
    proof: {
      ok: decision.proof.ok,
      truncated: decision.proof.truncated,
      targetSkeleton: decision.proof.targetSkeleton,
      provenDomains: decision.proof.provenDomains,
      witness: decision.proof.witness,
      reason: decision.proof.reason,
    },
  });
}

/**
 * Verify a previously recorded table summary (for example one shipped with a
 * bookmark or a policy bundle) against the tables currently loaded.
 */
export function verifyTableSummary(
  expected: TableSummary,
  tables: VersionedTables,
  options: { readonly allowExtraTables?: boolean } = {},
): VerificationReport {
  const actual = summarizeTables(tables);
  const errors: string[] = [];
  let checkedTables = 0;
  let exactKeySet = true;
  for (const kind of ["confusables", "scripts", "suffixes"] as const) {
    const expectedEntries = expected[kind] as Readonly<
      Record<
        string,
        {
          digest: string;
          version: string;
          ruleCount?: number;
          rangeCount?: number;
        }
      >
    >;
    const actualEntries = actual[kind] as Readonly<
      Record<
        string,
        {
          digest: string;
          version: string;
          ruleCount?: number;
          rangeCount?: number;
        }
      >
    >;
    for (const key of Object.keys(expectedEntries)) {
      checkedTables++;
      const value = actualEntries[key];
      if (!value) {
        errors.push(`${kind}.${key}: table is missing`);
        continue;
      }
      if (value.digest !== expectedEntries[key].digest) {
        errors.push(`${kind}.${key}: digest mismatch`);
      }
      if (value.version !== expectedEntries[key].version) {
        errors.push(`${kind}.${key}: version mismatch`);
      }
      if (
        expectedEntries[key].ruleCount !== undefined &&
        value.ruleCount !== expectedEntries[key].ruleCount
      ) {
        errors.push(`${kind}.${key}: rule count mismatch`);
      }
      if (
        expectedEntries[key].rangeCount !== undefined &&
        value.rangeCount !== expectedEntries[key].rangeCount
      ) {
        errors.push(`${kind}.${key}: range count mismatch`);
      }
    }
    for (const key of Object.keys(actualEntries)) {
      if (!expectedEntries[key]) {
        exactKeySet = false;
        if (!(options.allowExtraTables ?? false)) {
          errors.push(`${kind}.${key}: unexpected table`);
        }
      }
    }
  }
  if (exactKeySet || !(options.allowExtraTables ?? false)) {
    if (expected.combinedDigest !== actual.combinedDigest) {
      errors.push("combined table summary digest mismatch");
    }
  }
  return {
    ok: errors.length === 0,
    errors,
    checkedLabels: 0,
    checkedTables,
  };
}

/** Exported for checker tests; not part of the decision path. */
export function candidateWordsForCheck(
  request: DisplayRequest,
  limit = 20000,
): readonly string[] {
  if (!request.automaton) return [];
  const enumerated = enumerateAcceptedWords(
    request.automaton,
    Math.max(0, Math.min(64, request.maxCandidateScalars ?? 64)),
    limit,
  );
  if (enumerated.truncated) {
    throw new Error("candidate enumeration exceeded the checker limit");
  }
  return enumerated.words;
}
