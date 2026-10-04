import { Effect, ExceptionRule, Match, Request } from "./types";

export function matches(request: Request, match: Match): boolean {
  for (const key of Object.keys(match)) {
    const expected = match[key]!;
    const actual = request[key];
    if (actual === undefined) return false;
    if (Array.isArray(expected)) {
      if (!(expected as readonly string[]).includes(actual)) return false;
    } else if (actual !== expected) {
      return false;
    }
  }
  return true;
}

export function byPrecedence(a: ExceptionRule, b: ExceptionRule): number {
  if (a.priority !== b.priority) return a.priority - b.priority;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export function sortByPrecedence(rules: readonly ExceptionRule[]): ExceptionRule[] {
  return [...rules].sort(byPrecedence);
}

export interface Decision {
  readonly effect: Effect;
  readonly ruleIndex: number;
}

export function evaluate(
  request: Request,
  sortedRules: readonly ExceptionRule[],
  baseEffect: Effect
): Decision {
  for (let i = 0; i < sortedRules.length; i++) {
    if (matches(request, sortedRules[i]!.match)) {
      return { effect: sortedRules[i]!.effect, ruleIndex: i };
    }
  }
  return { effect: baseEffect, ruleIndex: -1 };
}
