import { compileGlob } from "./glob.ts";
import type { PathRequirement, Rule } from "./types.ts";

/**
 * Evaluate the ordered rule list against one path.
 * Rules are applied in input order (ascending priority):
 *   - "override" (default) replaces the accumulated owner set;
 *   - "append" unions its owners into the accumulated set.
 * The effective threshold always comes from the last matching rule.
 * The author is removed from the resulting owner set.
 * Returns null when no rule matches (path imposes no requirement).
 */
export function requirementForPath(
  path: string,
  rules: Rule[],
  author: string,
): PathRequirement | null {
  let owners = new Set<string>();
  let threshold = 1;
  let matched = false;
  for (const rule of rules) {
    if (!compileGlob(rule.pattern).test(path.replace(/^\/+/, ""))) continue;
    matched = true;
    if ((rule.mode ?? "override") === "override") {
      owners = new Set(rule.owners);
    } else {
      for (const owner of rule.owners) owners.add(owner);
    }
    threshold = rule.threshold ?? 1;
  }
  if (!matched) return null;
  owners.delete(author);
  return { path, owners: [...owners], threshold };
}

/**
 * Requirements for one changed file. A renamed file carries requirements
 * for both its old and new path; both must be satisfied.
 */
export function requirementsForFile(
  file: { path: string; oldPath?: string },
  rules: Rule[],
  author: string,
): PathRequirement[] {
  const requirements: PathRequirement[] = [];
  const current = requirementForPath(file.path, rules, author);
  if (current) requirements.push(current);
  if (file.oldPath !== undefined && file.oldPath !== file.path) {
    const previous = requirementForPath(file.oldPath, rules, author);
    if (previous) requirements.push(previous);
  }
  return requirements;
}
