import type {
  Condition,
  Decision,
  Input,
  Rule,
  RuleSet,
} from "./types.ts";

/**
 * Evaluate one condition against an input.
 *
 * Only the explicitly declared attributes on `input` are read. Missing
 * attributes yield `false` for membership/comparison tests. There is no
 * access to external identity services, network, time or environment.
 */
export function evalCondition(cond: Condition, input: Input): boolean {
  switch (cond.kind) {
    case "in": {
      if (!Object.hasOwn(input, cond.attr)) return false;
      const value = input[cond.attr];
      return cond.values.includes(value as string | number | boolean);
    }
    case "int": {
      if (!Object.hasOwn(input, cond.attr)) return false;
      const value = input[cond.attr];
      if (typeof value !== "number" || !Number.isInteger(value)) return false;
      switch (cond.op) {
        case "=": return value === cond.value;
        case "!=": return value !== cond.value;
        case "<": return value < cond.value;
        case "<=": return value <= cond.value;
        case ">": return value > cond.value;
        case ">=": return value >= cond.value;
      }
      break;
    }
    case "and":
      return cond.of.every((child) => evalCondition(child, input));
    case "or":
      return cond.of.some((child) => evalCondition(child, input));
    case "not":
      return !evalCondition(cond.of, input);
  }
}

/**
 * Evaluate a rule set with order-independent deny-overrides semantics:
 * collect every matching rule; if any deny rule matches the result is
 * deny, otherwise any matching allow yields allow, otherwise the
 * configured default effect applies.
 *
 * Because the rule collection is a set operation, reordering the rules
 * or commuting operands inside AND/OR cannot change the effect.
 */
export function evalRuleSet(rs: RuleSet, input: Input): Decision {
  const matchedAllow: string[] = [];
  const matchedDeny: string[] = [];
  for (const rule of rs.rules as readonly Rule[]) {
    if (evalCondition(rule.when, input)) {
      if (rule.effect === "deny") matchedDeny.push(rule.id);
      else matchedAllow.push(rule.id);
    }
  }
  const defaulted = matchedAllow.length === 0 && matchedDeny.length === 0;
  const effect =
    matchedDeny.length > 0
      ? "deny"
      : matchedAllow.length > 0
        ? "allow"
        : rs.defaultEffect;
  const matchedRuleIds =
    effect === "deny"
      ? [...matchedDeny].sort()
      : [...matchedAllow].sort();
  return { effect, matchedRuleIds, defaulted };
}
