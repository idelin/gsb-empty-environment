import { Rope } from "./rope";
import { applyCaseTable, CaseTable, defaultCaseTable } from "./casing";

export interface Rule {
  /** Deterministic tie-breaker after priority. */
  id: number;
  /** Word to match. An empty pattern disables the rule. */
  pattern: string;
  replacement: string;
  /** Higher priority wins conflicts. Defaults to 0. */
  priority?: number;
  /** Disabled rules never match. Defaults to true. */
  enabled?: boolean;
  /**
   * When false (default), matching is case-insensitive and the
   * replacement is recased through the case table.
   */
  caseSensitive?: boolean;
}

export interface Candidate {
  ruleId: number;
  priority: number;
  /** Half-open span in the ORIGINAL text. */
  start: number;
  end: number;
  /** Original text that was matched. */
  matched: string;
  /** Replacement after case-table adjustment. */
  replacement: string;
}

export interface ReplaceResult {
  text: string;
  /** Adopted candidates, sorted by start. */
  adopted: Candidate[];
  /** Candidates that lost an overlap conflict, sorted by start. */
  shadowed: Candidate[];
  /** Ids of rules that were disabled (empty pattern or enabled: false). */
  disabledRules: number[];
}

export interface ReplaceOptions {
  caseTable?: CaseTable;
  /** Word-character test used for word boundaries. */
  isWordChar?: (ch: string) => boolean;
}

const DEFAULT_WORD_CHAR = /[\p{L}\p{N}_]/u;

function defaultIsWordChar(ch: string): boolean {
  return ch.length > 0 && DEFAULT_WORD_CHAR.test(ch);
}

/** Readable view over either a plain string or a Rope. */
interface Source {
  length: number;
  charAt(i: number): string;
  slice(a: number, b?: number): string;
}

function asSource(text: string | Rope): Source {
  if (typeof text === "string") {
    return {
      length: text.length,
      charAt: (i) => text.charAt(i),
      slice: (a, b) => text.slice(a, b),
    };
  }
  return text;
}

function isDisabled(rule: Rule): boolean {
  return rule.enabled === false || rule.pattern.length === 0;
}

/**
 * Collect every match of every active rule against the ORIGINAL text.
 * Matches are never recomputed against replaced output, so a replacement
 * can never trigger another rule (no cascading).
 */
export function findCandidates(
  text: string | Rope,
  rules: Rule[],
  options: ReplaceOptions = {},
): { candidates: Candidate[]; disabledRules: number[] } {
  const src = asSource(text);
  const isWordChar = options.isWordChar ?? defaultIsWordChar;
  const caseTable = options.caseTable ?? defaultCaseTable;

  const active = rules.filter((r) => !isDisabled(r));
  const disabledRules = rules.filter(isDisabled).map((r) => r.id);
  const candidates: Candidate[] = [];

  for (let pos = 0; pos < src.length; pos++) {
    if (isWordChar(src.charAt(pos - 1))) continue; // left word boundary
    for (const rule of active) {
      const pattern = rule.pattern;
      if (pos + pattern.length > src.length) continue;
      let ok = true;
      if (rule.caseSensitive) {
        for (let k = 0; k < pattern.length; k++) {
          if (src.charAt(pos + k) !== pattern.charAt(k)) {
            ok = false;
            break;
          }
        }
      } else {
        for (let k = 0; k < pattern.length; k++) {
          if (src.charAt(pos + k).toLowerCase() !== pattern.charAt(k).toLowerCase()) {
            ok = false;
            break;
          }
        }
      }
      if (!ok) continue;
      const end = pos + pattern.length;
      if (isWordChar(src.charAt(end))) continue; // right word boundary
      const matched = src.slice(pos, end);
      const replacement = rule.caseSensitive
        ? rule.replacement
        : applyCaseTable(matched, rule.replacement, caseTable);
      candidates.push({
        ruleId: rule.id,
        priority: rule.priority ?? 0,
        start: pos,
        end,
        matched,
        replacement,
      });
    }
  }
  return { candidates, disabledRules };
}

/**
 * Resolve overlaps: sort by priority (desc), then rule id (asc), then
 * position; greedily accept non-overlapping candidates. Losers are
 * reported as shadowed.
 */
export function resolveConflicts(candidates: Candidate[]): {
  adopted: Candidate[];
  shadowed: Candidate[];
} {
  const ordered = [...candidates].sort(
    (a, b) =>
      b.priority - a.priority ||
      a.ruleId - b.ruleId ||
      a.start - b.start ||
      a.end - b.end,
  );
  const adopted: Candidate[] = [];
  const shadowed: Candidate[] = [];
  for (const cand of ordered) {
    const overlaps = adopted.some((a) => cand.start < a.end && a.start < cand.end);
    if (overlaps) shadowed.push(cand);
    else adopted.push(cand);
  }
  const byStart = (a: Candidate, b: Candidate) =>
    a.start - b.start || a.end - b.end || a.ruleId - b.ruleId;
  adopted.sort(byStart);
  shadowed.sort(byStart);
  return { adopted, shadowed };
}

export function replace(
  text: string | Rope,
  rules: Rule[],
  options: ReplaceOptions = {},
): ReplaceResult {
  const src = asSource(text);
  const { candidates, disabledRules } = findCandidates(src as string | Rope, rules, options);
  const { adopted, shadowed } = resolveConflicts(candidates);

  let out = "";
  let cursor = 0;
  for (const cand of adopted) {
    out += src.slice(cursor, cand.start) + cand.replacement;
    cursor = cand.end;
  }
  out += src.slice(cursor);

  return { text: out, adopted, shadowed, disabledRules };
}
