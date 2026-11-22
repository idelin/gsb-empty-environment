import {
  createConfusableMatcher,
  createConfusableProgram,
  transcribe,
} from "./confusables.ts";
import { decideDisplay } from "./decide.ts";
import { verifyDecision } from "./checker.ts";
import {
  assertScriptTable,
  effectiveScripts,
  scriptPolicyHolds,
} from "./scripts.ts";
import {
  effectiveSuffix,
  parseSuffixRule,
  type ParsedSuffixRule,
} from "./suffix.ts";
import type {
  CheckResult,
  ConfusableRule,
  ConfusableTable,
  DisplayRequest,
  ExhaustiveCheckOptions,
  ExhaustiveReport,
  Scalar,
  ScriptPolicy,
  ScriptTable,
  SuffixTable,
} from "./types.ts";

export interface SuffixCase {
  readonly labels: readonly string[];
  readonly expectedSuffix: string;
}

export interface VersionFallbackCase {
  readonly request: DisplayRequest;
  readonly newVersion: string;
  readonly oldVersion: string;
}

export interface ExhaustiveConfig extends ExhaustiveCheckOptions {
  readonly confusables: ConfusableTable;
  readonly alternateConfusables?: ConfusableTable;
  readonly scripts: ScriptTable;
  readonly policies?: readonly ScriptPolicy[];
  readonly suffixTable: SuffixTable;
  readonly suffixCases?: readonly SuffixCase[];
  readonly labelAlphabet?: readonly string[];
  readonly versionFallback?: VersionFallbackCase;
}

const DEFAULT_ALPHABET: readonly Scalar[] = [
  0x61,
  0x62,
  0x63,
  0x41,
  0x3b1,
  0x430,
  0x1f600,
];

function limitedPush(
  failures: string[],
  message: string,
  limit = 25,
): void {
  if (failures.length < limit) failures.push(message);
}

export function exhaustiveWords(
  alphabet: readonly Scalar[],
  maxWordLength: number,
  maxWords = 10000,
): Scalar[][] {
  const depthLimit = Number.isFinite(maxWordLength)
    ? Math.max(0, Math.floor(maxWordLength))
    : 0;
  const wordLimit = Number.isFinite(maxWords)
    ? Math.max(1, Math.floor(maxWords))
    : 10000;
  const words: Scalar[][] = [];
  const current: Scalar[] = [];
  const walk = (): void => {
    if (words.length >= wordLimit) return;
    words.push(current.slice());
    if (current.length >= depthLimit) return;
    for (const scalar of alphabet) {
      current.push(scalar);
      walk();
      current.pop();
      if (words.length >= wordLimit) return;
    }
  };
  walk();
  return words;
}

function allChunkings(
  word: readonly Scalar[],
  maxChunkings: number,
): Scalar[][][] {
  const limit = Number.isFinite(maxChunkings)
    ? Math.max(1, Math.floor(maxChunkings))
    : 16;
  const output: Scalar[][][] = [];
  const chunks: Scalar[][] = [];
  const walk = (start: number): void => {
    if (output.length >= limit) return;
    if (start === word.length) {
      output.push(chunks.map((chunk) => chunk.slice()));
      return;
    }
    for (let end = start + 1; end <= word.length; end++) {
      chunks.push(word.slice(start, end));
      walk(end);
      chunks.pop();
      if (output.length >= limit) return;
    }
  };
  walk(0);
  return output;
}

/**
 * Deliberately simple reference implementation: scan every rule at every
 * position, prefer the longest consume sequence, and use declaration order to
 * break equal-length ties.
 */
export function referenceTranscribe(
  rules: readonly ConfusableRule[],
  word: readonly Scalar[],
): Scalar[] {
  const output: Scalar[] = [];
  let index = 0;
  while (index < word.length) {
    let bestLength = 0;
    let bestSkeleton: readonly Scalar[] | null = null;
    const maxLength = Math.min(3, word.length - index);
    for (let length = 1; length <= maxLength; length++) {
      for (const rule of rules) {
        if (rule.consumes.length !== length) continue;
        let matches = true;
        for (let i = 0; i < length; i++) {
          if (rule.consumes[i] !== word[index + i]) {
            matches = false;
            break;
          }
        }
        if (matches) {
          if (length > bestLength) {
            bestLength = length;
            bestSkeleton = rule.skeleton;
          }
          break;
        }
      }
    }
    if (bestSkeleton !== null) {
      output.push(...bestSkeleton);
      index += bestLength;
    } else {
      output.push(word[index]);
      index += 1;
    }
  }
  return output;
}

function sameScalars(
  a: readonly Scalar[],
  b: readonly Scalar[],
): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}

function scalarWordText(word: readonly Scalar[]): string {
  return JSON.stringify(word);
}

export function checkConfusableTable(
  table: ConfusableTable,
  options: ExhaustiveCheckOptions = {},
): CheckResult {
  const alphabet = options.alphabet ?? DEFAULT_ALPHABET;
  const maxWordLength = options.maxWordLength ?? 4;
  const maxWords = options.maxWords ?? 3000;
  const maxChunkings = options.maxChunkings ?? 16;
  const failures: string[] = [];
  let checked = 0;
  const program = createConfusableProgram(table);
  const words = exhaustiveWords(alphabet, maxWordLength, maxWords);

  for (const word of words) {
    const expected = referenceTranscribe(table.rules, word);
    const direct = transcribe(program, word);
    checked++;
    if (!sameScalars(direct, expected)) {
      limitedPush(
        failures,
        `${scalarWordText(word)}: batch ${JSON.stringify(direct)} != reference ${JSON.stringify(expected)}`,
      );
      continue;
    }

    for (const chunks of allChunkings(word, maxChunkings)) {
      const matcher = createConfusableMatcher(program);
      const actual: Scalar[] = [];
      for (const chunk of chunks) actual.push(...matcher.push(chunk));
      actual.push(...matcher.end());
      checked++;
      if (!sameScalars(actual, expected)) {
        limitedPush(
          failures,
          `${scalarWordText(word)} chunks ${JSON.stringify(chunks)}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`,
        );
      }
      if (matcher.pending().length !== 0) {
        limitedPush(
          failures,
          `${scalarWordText(word)}: end() left pending scalars`,
        );
      }
    }

    const text = String.fromCodePoint(...word);
    for (let split = 1; split < text.length; split++) {
      const matcher = createConfusableMatcher(program);
      const actual: Scalar[] = [];
      actual.push(...matcher.push(text.slice(0, split)));
      actual.push(...matcher.push(text.slice(split)));
      actual.push(...matcher.end());
      checked++;
      if (!sameScalars(actual, expected)) {
        limitedPush(
          failures,
          `${scalarWordText(word)} UTF-16 split ${split}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`,
        );
      }
    }
  }

  return {
    name: "confusable-streaming-longest-match",
    ok: failures.length === 0,
    checked,
    failures,
  };
}

export function checkLongestMatchChanges(
  before: ConfusableTable,
  after: ConfusableTable,
  options: ExhaustiveCheckOptions = {},
): CheckResult {
  const alphabet = options.alphabet ?? DEFAULT_ALPHABET;
  const maxWordLength = options.maxWordLength ?? 4;
  const maxWords = options.maxWords ?? 3000;
  const failures: string[] = [];
  let checked = 0;
  let differences = 0;
  const beforeProgram = createConfusableProgram(before);
  const afterProgram = createConfusableProgram(after);

  for (const word of exhaustiveWords(alphabet, maxWordLength, maxWords)) {
    const referenceBefore = referenceTranscribe(before.rules, word);
    const referenceAfter = referenceTranscribe(after.rules, word);
    const actualBefore = transcribe(beforeProgram, word);
    const actualAfter = transcribe(afterProgram, word);
    checked++;
    if (!sameScalars(referenceBefore, referenceAfter)) differences++;
    if (!sameScalars(actualBefore, referenceBefore)) {
      limitedPush(
        failures,
        `before ${scalarWordText(word)}: ${JSON.stringify(actualBefore)} != ${JSON.stringify(referenceBefore)}`,
      );
    }
    if (!sameScalars(actualAfter, referenceAfter)) {
      limitedPush(
        failures,
        `after ${scalarWordText(word)}: ${JSON.stringify(actualAfter)} != ${JSON.stringify(referenceAfter)}`,
      );
    }
  }

  return {
    name: `longest-match-changes (${differences} observed)`,
    ok: failures.length === 0,
    checked,
    failures,
  };
}

export function checkEmptyOutput(
  table: ConfusableTable,
  options: ExhaustiveCheckOptions = {},
): CheckResult {
  const failures: string[] = [];
  let checked = 0;
  const emptyRules = table.rules.filter((rule) => rule.skeleton.length === 0);
  if (emptyRules.length === 0) {
    return {
      name: "empty-output (no empty rules)",
      ok: true,
      checked: 0,
      failures,
    };
  }
  const program = createConfusableProgram(table);
  for (const rule of emptyRules) {
    const word = rule.consumes.slice();
    const expected = referenceTranscribe(table.rules, word);
    const actual = transcribe(program, word);
    checked++;
    if (!sameScalars(actual, expected)) {
      limitedPush(
        failures,
        `${scalarWordText(word)}: ${JSON.stringify(actual)} != ${JSON.stringify(expected)}`,
      );
    }
    if (
      expected.length === 0 &&
      actual.length !== 0
    ) {
      limitedPush(
        failures,
        `${scalarWordText(word)}: empty-output rule emitted symbols`,
      );
    }
  }
  const base = checkConfusableTable(table, options);
  checked += base.checked;
  failures.push(...base.failures);
  return {
    name: "empty-output",
    ok: failures.length === 0,
    checked,
    failures,
  };
}

function bruteForceScriptPolicy(
  table: ScriptTable,
  policy: ScriptPolicy,
  word: readonly Scalar[],
): boolean {
  const allowed = new Set(policy.allowed);
  const disallowed = new Set(policy.disallowed ?? []);
  const choices: string[][] = [];
  for (const scalar of word) {
    const effective = effectiveScripts(table, scalar);
    if (effective.length === 0) continue;
    const options = [
      ...new Set(
        effective.filter(
          (script) => allowed.has(script) && !disallowed.has(script),
        ),
      ),
    ];
    if (options.length === 0) return false;
    choices.push(options);
  }
  if (choices.length === 0) return policy.allowNeutralOnly ?? true;

  let valid = false;
  const chosen: string[] = [];
  const walk = (index: number): void => {
    if (valid) return;
    if (index === choices.length) {
      const scripts = new Set(chosen);
      if ((policy.singleScript ?? true) && scripts.size > 1) return;
      const min = policy.minScripts ?? 1;
      const max = policy.maxScripts ?? Number.POSITIVE_INFINITY;
      if (scripts.size >= min && scripts.size <= max) valid = true;
      return;
    }
    for (const script of choices[index]) {
      chosen.push(script);
      walk(index + 1);
      chosen.pop();
      if (valid) return;
    }
  };
  walk(0);
  return valid;
}

export function checkScriptExtensions(
  table: ScriptTable,
  policies: readonly ScriptPolicy[],
  options: ExhaustiveCheckOptions = {},
): CheckResult {
  assertScriptTable(table);
  const alphabet = options.alphabet ?? DEFAULT_ALPHABET;
  const maxWordLength = options.maxWordLength ?? 4;
  const maxWords = options.maxWords ?? 3000;
  const failures: string[] = [];
  let checked = 0;
  for (const word of exhaustiveWords(alphabet, maxWordLength, maxWords)) {
    for (let index = 0; index < policies.length; index++) {
      const policy = policies[index];
      const actual = scriptPolicyHolds(table, policy, word);
      const expected = bruteForceScriptPolicy(table, policy, word);
      checked++;
      if (actual !== expected) {
        limitedPush(
          failures,
          `policy ${index} ${scalarWordText(word)}: ${actual} != ${expected}`,
        );
      }
    }
  }
  return {
    name: "script-extension-sets",
    ok: failures.length === 0,
    checked,
    failures,
  };
}

function expectedSuffixLabels(
  labels: readonly string[],
  parsed: readonly ParsedSuffixRule[],
): string[] {
  const normalized = labels.map((label) => label.normalize("NFC").toLowerCase());
  const tailMatches = (tail: readonly string[]): boolean => {
    if (tail.length > normalized.length) return false;
    const offset = normalized.length - tail.length;
    for (let i = 0; i < tail.length; i++) {
      if (normalized[offset + i] !== tail[i]) return false;
    }
    return true;
  };

  let exception: ParsedSuffixRule | null = null;
  for (const rule of parsed) {
    if (rule.kind !== "exception" || !tailMatches(rule.labels)) continue;
    if (!exception || rule.labels.length > exception.labels.length) {
      exception = rule;
    }
  }
  if (exception) {
    return normalized.slice(-(exception.labels.length - 1));
  }

  let count = 0;
  for (const rule of parsed) {
    if (rule.kind === "exception") continue;
    if (!tailMatches(rule.labels)) continue;
    let candidate: number;
    if (rule.kind === "wildcard") {
      if (normalized.length < rule.labels.length + 1) continue;
      candidate = rule.labels.length + 1;
    } else {
      candidate = rule.labels.length;
    }
    if (candidate > count) count = candidate;
  }
  if (count === 0) count = normalized.length > 0 ? 1 : 0;
  return normalized.slice(-count);
}

export function checkSuffixExceptions(
  table: SuffixTable,
  cases: readonly SuffixCase[] = [],
  options: ExhaustiveCheckOptions & {
    readonly labelAlphabet?: readonly string[];
  } = {},
): CheckResult {
  const failures: string[] = [];
  let checked = 0;
  const parsed = table.rules.map((rule, index) => parseSuffixRule(rule, index));
  const labelAlphabet =
    options.labelAlphabet ??
    [
      ...new Set([
        ...parsed.flatMap((rule) => rule.labels),
        "a",
        "b",
      ]),
    ];
  const maxLabelDepth = options.maxWordLength ?? 4;
  const maxCases = options.maxWords ?? 3000;

  for (const test of cases) {
    const actual = effectiveSuffix(test.labels, table);
    checked++;
    if (actual.suffix !== test.expectedSuffix) {
      limitedPush(
        failures,
        `${test.labels.join(".")}: '${actual.suffix}' != '${test.expectedSuffix}'`,
      );
    }
  }

  const current: string[] = [];
  let generated = 0;
  const walk = (): void => {
    if (generated >= maxCases) return;
    if (current.length > 0) {
      generated++;
      const expected = expectedSuffixLabels(current, parsed).join(".");
      const actual = effectiveSuffix(current, table).suffix;
      checked++;
      if (actual !== expected) {
        limitedPush(
          failures,
          `generated ${current.join(".")}: '${actual}' != '${expected}'`,
        );
      }
    }
    if (current.length >= maxLabelDepth) return;
    for (const label of labelAlphabet) {
      current.push(label);
      walk();
      current.pop();
      if (generated >= maxCases) return;
    }
  };
  walk();

  for (const rule of parsed) {
    if (rule.kind !== "exception") continue;
    const probe = ["a", ...rule.labels];
    const actual = effectiveSuffix(probe, table);
    const expected = rule.labels.slice(1).join(".");
    checked++;
    if (actual.suffix !== expected || actual.kind !== "exception") {
      limitedPush(
        failures,
        `exception ${rule.rule.suffix}: '${actual.suffix}'/'${actual.kind}' != '${expected}'/exception`,
      );
    }
  }

  return {
    name: "suffix-exceptions",
    ok: failures.length === 0,
    checked,
    failures,
  };
}

export function checkVersionFallback(
  test: VersionFallbackCase,
): CheckResult {
  const failures: string[] = [];
  let checked = 0;
  const both = decideDisplay({
    ...test.request,
    versionOrder: [test.newVersion, test.oldVersion],
    versionFallback: true,
  });
  checked++;
  if (both.attempts.length !== 2) {
    failures.push("expected two version attempts");
  } else {
    if (both.attempts[0].ok) {
      failures.push(`new version '${test.newVersion}' unexpectedly proved`);
    }
    if (!both.attempts[1].ok) {
      failures.push(`old version '${test.oldVersion}' did not prove`);
    }
  }
  if (both.version !== test.oldVersion) {
    failures.push(`fallback selected '${both.version}' instead of old version`);
  }

  const newOnly = decideDisplay({
    ...test.request,
    versionOrder: [test.newVersion],
  });
  checked++;
  if (newOnly.proof.ok) {
    failures.push("new-version-only decision unexpectedly proved");
  }
  for (const label of newOnly.labels) {
    const nonAscii = Array.from(label.label).some(
      (character) => (character.codePointAt(0) as number) > 0x7f,
    );
    if (nonAscii && label.mode !== "ascii") {
      failures.push(`new-version-only label '${label.label}' did not fall back`);
    }
  }

  const oldOnly = decideDisplay({
    ...test.request,
    versionOrder: [test.oldVersion],
  });
  checked++;
  if (!oldOnly.proof.ok) {
    failures.push("old-version-only decision did not prove");
  }

  for (const [name, decision] of [
    ["both", both],
    ["new", newOnly],
    ["old", oldOnly],
  ] as const) {
    const report = verifyDecision(decision, {
      ...test.request,
      versionOrder:
        name === "both"
          ? [test.newVersion, test.oldVersion]
          : name === "new"
            ? [test.newVersion]
            : [test.oldVersion],
      ...(name === "both" ? { versionFallback: true } : {}),
    });
    checked++;
    if (!report.ok) {
      failures.push(
        `${name} decision failed verification: ${report.errors.join("; ")}`,
      );
    }
  }

  return {
    name: "old-version-only-aliases",
    ok: failures.length === 0,
    checked,
    failures,
  };
}

export function runExhaustiveChecks(
  config: ExhaustiveConfig,
): ExhaustiveReport {
  const options: ExhaustiveCheckOptions = {
    ...(config.alphabet ? { alphabet: config.alphabet } : {}),
    ...(config.maxWordLength !== undefined
      ? { maxWordLength: config.maxWordLength }
      : {}),
    ...(config.maxChunkings !== undefined
      ? { maxChunkings: config.maxChunkings }
      : {}),
    ...(config.maxWords !== undefined ? { maxWords: config.maxWords } : {}),
  };
  const checks: CheckResult[] = [
    checkConfusableTable(config.confusables, options),
    checkEmptyOutput(config.confusables, options),
    checkScriptExtensions(
      config.scripts,
      config.policies ??
        [
          ...(config.suffixTable.defaultPolicy
            ? [config.suffixTable.defaultPolicy]
            : []),
          ...config.suffixTable.rules
            .map((rule) => rule.policy)
            .filter((policy): policy is ScriptPolicy => policy !== undefined),
        ],
      options,
    ),
    checkSuffixExceptions(config.suffixTable, config.suffixCases ?? [], {
      ...options,
      ...(config.labelAlphabet ? { labelAlphabet: config.labelAlphabet } : {}),
    }),
  ];
  if (config.alternateConfusables) {
    checks.push(
      checkLongestMatchChanges(
        config.confusables,
        config.alternateConfusables,
        options,
      ),
    );
  }
  if (config.versionFallback) {
    checks.push(checkVersionFallback(config.versionFallback));
  }
  return {
    ok: checks.every((check) => check.ok),
    checks,
  };
}
