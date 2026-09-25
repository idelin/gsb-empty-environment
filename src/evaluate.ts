import type {
  AttrInput,
  Condition,
  EvalResult,
  Policy,
} from "./model.ts";

export function evalCondition(cond: Condition, input: AttrInput): boolean {
  switch (cond.op) {
    case "always":
      return true;
    case "eq":
      return input[cond.attr] === cond.value;
    case "neq":
      return input[cond.attr] !== cond.value;
    case "in":
      return cond.values.includes(input[cond.attr]);
    case "lt":
      return Number(input[cond.attr]) < cond.value;
    case "le":
      return Number(input[cond.attr]) <= cond.value;
    case "gt":
      return Number(input[cond.attr]) > cond.value;
    case "ge":
      return Number(input[cond.attr]) >= cond.value;
    case "between": {
      const v = Number(input[cond.attr]);
      return v >= cond.min && v <= cond.max;
    }
    case "and":
      return cond.args.every((c) => evalCondition(c, input));
    case "or":
      return cond.args.some((c) => evalCondition(c, input));
    case "not":
      return !evalCondition(cond.arg, input);
  }
}

export function evaluate(policy: Policy, input: AttrInput): EvalResult {
  const matchedAllow: string[] = [];
  const matchedDeny: string[] = [];
  const reasons = new Map<string, string>();
  for (const rule of policy.rules) {
    if (evalCondition(rule.when, input)) {
      (rule.effect === "allow" ? matchedAllow : matchedDeny).push(rule.id);
      reasons.set(rule.id, rule.reason);
    }
  }
  matchedAllow.sort();
  matchedDeny.sort();
  let decision: "allow" | "deny";
  if (policy.combining === "deny-overrides") {
    decision =
      matchedDeny.length > 0
        ? "deny"
        : matchedAllow.length > 0
          ? "allow"
          : policy.default;
  } else {
    decision =
      matchedAllow.length > 0
        ? "allow"
        : matchedDeny.length > 0
          ? "deny"
          : policy.default;
  }
  const matched = [...matchedAllow, ...matchedDeny].sort();
  return {
    decision,
    matched,
    reasons: matched.map((id) => `${id}: ${reasons.get(id)}`),
  };
}

export function explanationKey(result: EvalResult): string {
  return JSON.stringify({ matched: result.matched, reasons: result.reasons });
}
