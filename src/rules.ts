import { MappingRule } from "./types";

export function ruleOldIds(rule: MappingRule): string[] {
  return rule.kind === "merge" ? rule.from.slice() : [rule.from];
}

export function ruleNewIds(rule: MappingRule): string[] {
  return rule.kind === "split" ? rule.to.slice() : [rule.to];
}

export function ruleLabel(rule: MappingRule): string {
  return "explicit:" + rule.kind;
}
