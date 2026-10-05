import type { BlockingFile, CheckInput, CheckResult, FileReport, RequirementReport } from './types.js';
import { resolveRequirement } from './rules.js';
import { findMinSubset, popcount } from './solver.js';
import type { Constraint } from './solver.js';

export const MAX_CANDIDATES = 20;

export function checkApprovals(input : CheckInput) : CheckResult {
  const seen = new Set<string>();
  const candidates : string[] = [];
  for (const name of input.candidates) {
    if (name === input.author) continue;
    if (seen.has(name)) continue;
    seen.add(name);
    candidates.push(name);
  }
  if (candidates.length > MAX_CANDIDATES) {
    throw new Error('checkApprovals supports at most ' + MAX_CANDIDATES + ' candidates, got ' + candidates.length);
  }

  let defaultMin = 1;
  if (input.defaultMinApprovals !== undefined) defaultMin = input.defaultMinApprovals;

  const approvals = new Set<string>(input.approvals);
  approvals.delete(input.author);

  const candidateIndex = new Map<string, number>();
  candidates.forEach((name, idx) => { candidateIndex.set(name, idx); });

  const blocking : BlockingFile[] = [];
  const fileReports : FileReport[] = [];
  const needByMask = new Map<number, number>();

  for (const file of input.files) {
    const paths : string[] = [];
    if (file.oldPath !== undefined && file.oldPath !== file.path) paths.push(file.oldPath);
    paths.push(file.path);

    const reports : RequirementReport[] = [];
    for (const p of paths) {
      const req = resolveRequirement(input.rules, p, defaultMin);
      if (req === null) continue;
      const eligible = req.owners.filter((owner) => owner !== input.author);
      const approvedBy = eligible.filter((owner) => approvals.has(owner));
      const missing = Math.max(0, req.minApprovals - approvedBy.length);
      reports.push({
        path: p,
        owners: eligible,
        required: req.minApprovals,
        approvedBy: approvedBy,
        missing: missing,
      });
      if (req.minApprovals <= 0 || missing === 0) continue;

      let mask = 0;
      for (const owner of eligible) {
        const idx = candidateIndex.get(owner);
        if (idx !== undefined) mask = mask | (2 ** idx);
      }
      const available = popcount(mask);
      if (available < missing) {
        blocking.push({
          path: file.path,
          oldPath: file.oldPath,
          requirementPath: p,
          owners: eligible,
          requiredApprovals: req.minApprovals,
          existingApprovals: approvedBy.length,
          eligibleCandidates: available,
          shortfall: missing - available,
        });
      } else {
        const prev = needByMask.get(mask);
        if (prev === undefined || prev < missing) needByMask.set(mask, missing);
      }
    }
    fileReports.push({
      path: file.path,
      oldPath: file.oldPath,
      requirements: reports,
      satisfied: reports.every((r) => r.missing === 0),
    });
  }

  if (blocking.length > 0) {
    return { ok: false, addedApprovers: [], blockingFiles: blocking, files: fileReports };
  }

  const constraints : Constraint[] = [];
  needByMask.forEach((need, mask) => { constraints.push({ mask: mask, need: need }); });

  const solution = findMinSubset(candidates.length, constraints);
  if (solution < 0) {
    return { ok: false, addedApprovers: [], blockingFiles: [], files: fileReports };
  }

  const added : string[] = [];
  for (let i = 0; i < candidates.length; i += 1) {
    const name = candidates[i];
    if (name !== undefined && (solution & (2 ** i)) !== 0) added.push(name);
  }
  return { ok: true, addedApprovers: added, blockingFiles: [], files: fileReports };
}
