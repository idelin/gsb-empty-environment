import { Rope } from "./rope.ts";
import { applyCase } from "./casing.ts";

export interface Rule {
  /** Unique rule identifier; used as the final tie-breaker (ascending). */
  id: string;
  /** Word (or phrase) to match, matched case-insensitively on word boundaries. */
  pattern: string;
  /** Canonical (lowercase) replacement; case is derived from each match. */
  replacement: string;
  /** Higher priority wins conflicts. Defaults to 0. */
  priority?: number;
  /** Defaults to true. Rules with an empty pattern are always disabled. */
  enabled?: boolean;
}

export interface Candidate {
  ruleId: string;
  /** Start offset in the original text. */
  start: number;
  /** End offset (exclusive) in the original text. */
  end: number;
  /** Original text of the matched range. */
  matched: string;
  /** Replacement text with case mapped from the match. */
  replacement: string;
  /** For shadowed candidates, the id of the adopted rule that overlaps it. */
  shadowedBy: string | null;
}

export interface Edit {
  start: number;
  end: number;
  text: string;
}

export interface ReplaceResult {
  /** Text after applying all adopted replacements. */
  text: string;
  /** Adopted candidates, sorted by original start offset. */
  adopted: Candidate[];
  /** Shadowed (losing) candidates, sorted by original start offset. */
  shadowed: Candidate[];
  /** Forward edits in original coordinates. */
  edits: Edit[];
  /** Inverse edits: applyEdits(result.text, reverseEdits) restores the original. */
  reverseEdits: Edit[];
}

const WORD_CHAR = /[A-Za-z0-9_]/;

function isWordChar(ch: string | undefined): boolean {
  return ch !== undefined && WORD_CHAR.test(ch);
}

/** A rule only participates when enabled and its pattern is non-empty. */
export function isRuleActive(rule: Rule): boolean {
  return rule.enabled !== false && rule.pattern.length > 0;
}

/**
 * Yields every word-boundary-delimited, case-insensitive occurrence of
 * `patternLower` in the rope. Reads go through Rope.charAt, so matches
 * spanning chunk boundaries are found without flattening the rope.
 */
function* findMatches(
  rope: Rope,
  patternLower: string,
): Generator<{ start: number; end: number }> {
  const n = rope.length;
  const m = patternLower.length;
  for (let start = 0; start + m <= n; start += 1) {
    if (isWordChar(start > 0 ? rope.charAt(start - 1) : undefined)) continue;
    let ok = true;
    for (let k = 0; k < m; k += 1) {
      if (rope.charAt(start + k).toLowerCase() !== patternLower[k]) {
        ok = false;
        break;
      }
    }
    if (!ok) continue;
    const end = start + m;
    if (isWordChar(end < n ? rope.charAt(end) : undefined)) continue;
    yield { start, end };
  }
}

function compareIds(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/**
 * Applies all active rules to the original text simultaneously.
 *
 * Every rule is matched against the original text only, so a replacement
 * can never trigger another rule. Overlapping candidates conflict; the
 * winner is decided by priority (descending), then rule id (ascending).
 */
export function replaceBatch(rope: Rope, rules: readonly Rule[]): ReplaceResult {
  interface RankedCandidate extends Candidate {
    priority: number;
  }

  const all: RankedCandidate[] = [];
  for (const rule of rules) {
    if (!isRuleActive(rule)) continue;
    const patternLower = rule.pattern.toLowerCase();
    const replacementLower = rule.replacement.toLowerCase();
    const priority = rule.priority ?? 0;
    for (const { start, end } of findMatches(rope, patternLower)) {
      const matched = rope.slice(start, end);
      all.push({
        ruleId: rule.id,
        start,
        end,
        matched,
        replacement: applyCase(replacementLower, matched),
        shadowedBy: null,
        priority,
      });
    }
  }

  const ranked = [...all].sort(
    (a, b) =>
      b.priority - a.priority ||
      compareIds(a.ruleId, b.ruleId) ||
      a.start - b.start ||
      a.end - b.end,
  );

  const adoptedRanked: RankedCandidate[] = [];
  const shadowedRanked: RankedCandidate[] = [];
  for (const candidate of ranked) {
    const blocker = adoptedRanked.find(
      (a) => a.start < candidate.end && candidate.start < a.end,
    );
    if (blocker) {
      candidate.shadowedBy = blocker.ruleId;
      shadowedRanked.push(candidate);
    } else {
      adoptedRanked.push(candidate);
    }
  }

  const byPosition = (a: Candidate, b: Candidate): number => a.start - b.start || a.end - b.end;
  const strip = ({ priority: _priority, ...rest }: RankedCandidate): Candidate => rest;
  const adopted = adoptedRanked.sort(byPosition).map(strip);
  const shadowed = shadowedRanked.sort(byPosition).map(strip);

  const edits: Edit[] = adopted.map((c) => ({ start: c.start, end: c.end, text: c.replacement }));
  const text = applyEdits(rope.toString(), edits);

  const reverseEdits: Edit[] = [];
  let delta = 0;
  for (const edit of edits) {
    const start = edit.start + delta;
    reverseEdits.push({
      start,
      end: start + edit.text.length,
      text: rope.slice(edit.start, edit.end),
    });
    delta += edit.text.length - (edit.end - edit.start);
  }

  return { text, adopted, shadowed, edits, reverseEdits };
}

/**
 * Applies non-overlapping edits (in the coordinates of `text`) and returns
 * the patched string. Used for both the forward patch and the reverse patch.
 */
export function applyEdits(text: string, edits: readonly Edit[]): string {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  let out = "";
  let cursor = 0;
  for (const edit of sorted) {
    if (edit.start < cursor) {
      throw new RangeError(
        `overlapping edit at [${edit.start}, ${edit.end}); previous edit ends at ${cursor}`,
      );
    }
    out += text.slice(cursor, edit.start) + edit.text;
    cursor = edit.end;
  }
  return out + text.slice(cursor);
}
