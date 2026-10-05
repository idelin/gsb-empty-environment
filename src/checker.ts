import { requirementsForFile } from "./rules.ts";
import { solveMinimumSet, type Constraint } from "./solver.ts";
import type {
  BlockingFile,
  CheckerInput,
  CheckerOutput,
  FileReport,
} from "./types.ts";

export const MAX_CANDIDATES = 20;

function dedupe(values: string[]): string[] {
  return [...new Set(values)];
}

export function validateInput(input: CheckerInput): string[] {
  const errors: string[] = [];
  if (typeof input.author !== "string" || input.author.length === 0) {
    errors.push("author must be a non-empty string");
  }
  if (!Array.isArray(input.files)) {
    errors.push("files must be an array");
  } else {
    input.files.forEach((file, i) => {
      if (typeof file?.path !== "string" || file.path.length === 0) {
        errors.push(`files[${i}].path must be a non-empty string`);
      }
    });
  }
  if (!Array.isArray(input.rules)) {
    errors.push("rules must be an array");
  } else {
    input.rules.forEach((rule, i) => {
      if (typeof rule?.pattern !== "string" || rule.pattern.length === 0) {
        errors.push(`rules[${i}].pattern must be a non-empty string`);
      }
      if (!Array.isArray(rule?.owners)) {
        errors.push(`rules[${i}].owners must be an array`);
      }
      if (
        rule?.threshold !== undefined &&
        (!Number.isInteger(rule.threshold) || rule.threshold < 1)
      ) {
        errors.push(`rules[${i}].threshold must be a positive integer`);
      }
      const mode = rule?.mode ?? "override";
      if (mode !== "override" && mode !== "append") {
        errors.push(`rules[${i}].mode must be "override" or "append"`);
      }
    });
  }
  const candidates = dedupe(input.candidates ?? []);
  if (candidates.length > MAX_CANDIDATES) {
    errors.push(
      `candidates must contain at most ${MAX_CANDIDATES} entries (got ${candidates.length})`,
    );
  }
  return errors;
}

export function checkApprovals(input: CheckerInput): CheckerOutput {
  const errors = validateInput(input);
  if (errors.length > 0) {
    throw new Error(`invalid input: ${errors.join("; ")}`);
  }

  const author = input.author;
  // The author's own approval never counts.
  const approvers = dedupe(input.approvals ?? []).filter((u) => u !== author);
  const candidates = dedupe(input.candidates ?? []).filter(
    (u) => u !== author && !approvers.includes(u),
  );
  const approverSet = new Set(approvers);

  const constraints: Constraint[] = [];
  const fileRequirements = input.files.map((file, fileIndex) => {
    const requirements = requirementsForFile(file, input.rules, author);
    for (const requirement of requirements) {
      const ownerSet = new Set(requirement.owners);
      constraints.push({
        fileIndex,
        requirement,
        base: requirement.owners.filter((o) => approverSet.has(o)).length,
        ownerCandidates: candidates.flatMap((cand, i) =>
          ownerSet.has(cand) ? [i] : [],
        ),
      });
    }
    return requirements;
  });

  const chosen = solveMinimumSet(candidates, constraints);

  if (chosen === null) {
    const blocking: BlockingFile[] = [];
    const allAvailable = new Set([...approvers, ...candidates]);
    input.files.forEach((file, fileIndex) => {
      for (const requirement of fileRequirements[fileIndex]) {
        const maxApprovals = requirement.owners.filter((o) =>
          allAvailable.has(o),
        ).length;
        if (maxApprovals < requirement.threshold) {
          blocking.push({
            path: file.path,
            failedPath: requirement.path,
            owners: requirement.owners,
            threshold: requirement.threshold,
            maxApprovals,
          });
        }
      }
    });
    return { ok: false, added: [], approvers, files: [], blocking };
  }

  const added = chosen.map((i) => candidates[i]);
  const effective = new Set([...approvers, ...added]);

  const files: FileReport[] = input.files.map((file, fileIndex) => {
    const report: FileReport = {
      path: file.path,
      requirements: fileRequirements[fileIndex].map((requirement) => {
        const approvals = requirement.owners.filter((o) =>
          effective.has(o),
        ).length;
        return {
          ...requirement,
          approvals,
          satisfied: approvals >= requirement.threshold,
        };
      }),
      satisfied: true,
    };
    if (file.oldPath !== undefined) report.oldPath = file.oldPath;
    report.satisfied = report.requirements.every((r) => r.satisfied);
    return report;
  });

  return {
    ok: files.every((f) => f.satisfied),
    added,
    approvers: [...effective],
    files,
    blocking: [],
  };
}
