export type Effect = "allow" | "deny";

export type AttributeDomain = Record<string, readonly string[]>;

export type Request = Record<string, string>;

export type Match = Record<string, string | readonly string[]>;

export interface ExceptionRule {
  readonly id: string;
  readonly effect: Effect;
  readonly priority: number;
  readonly cost: number;
  readonly match: Match;
  readonly description?: string;
}

export interface PropertyConstraint {
  readonly id: string;
  readonly match: Match;
  readonly must: Effect;
  readonly description?: string;
}

export interface Policy {
  readonly baseEffect: Effect;
  readonly exceptions: readonly ExceptionRule[];
  readonly property: readonly PropertyConstraint[];
  readonly domain: AttributeDomain;
}

export const MAX_EXCEPTIONS = 24;
