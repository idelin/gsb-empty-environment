/** Three-valued truth: conditions over incomplete facts may be undecidable. */
export type Tri = true | false | 'unknown';

export type Action = 'backup' | 'exclude';

/** Inclusive numeric range; missing bound means unbounded. */
export interface Range {
  min?: number;
  max?: number;
}

export interface Condition {
  /** Glob the file path must match (supports `*`, `**`, `?`). */
  path?: string;
  /** Negated glob: the file path must NOT match. */
  pathNot?: string;
  /** Inclusive size range in bytes. */
  size?: Range;
  /** Inclusive modification-version range. */
  version?: Range;
  /** File must carry ALL of these tags. */
  tagsAll?: string[];
  /** Negated tag condition: file must carry NONE of these tags. */
  tagsNone?: string[];
}

export interface Rule {
  id: string;
  /** Higher priority wins when several rules match. */
  priority: number;
  condition: Condition;
  action: Action;
}

/**
 * Facts known about a file. Any fact may be absent (unknown);
 * unknown facts never silently fail a rule — they yield `unknown`.
 */
export interface FileFacts {
  path: string;
  size?: number;
  version?: number;
  tags?: string[];
}

export type Decision =
  | { kind: 'decided'; action: Action; ruleId: string }
  | { kind: 'unknown'; pendingRuleIds: string[] }
  | { kind: 'default'; action: Action };

/** Effective action of a decision; undecided files stay 'unknown'. */
export function effectiveAction(d: Decision): Action | 'unknown' {
  return d.kind === 'unknown' ? 'unknown' : d.action;
}
