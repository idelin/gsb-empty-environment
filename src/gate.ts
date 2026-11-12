import { createHash } from "node:crypto";
import { deriveSubpath, resolveExports } from "./resolver.ts";
import type {
  GateInput,
  GateResult,
  ProbeResult,
  ReviewVerdict,
} from "./types.ts";

export function sha256Hex(content: Uint8Array): string {
  return createHash("sha256").update(content).digest("hex");
}

function stripDotSlash(path: string): string {
  return path.startsWith("./") ? path.slice(2) : path;
}

/**
 * 条件差分构建网关：
 * 1. 仅用基线条件解析一次；
 * 2. 附加条件先经允许集合过滤，再与基线合并解析第二次；
 * 3. 入口相同直接放行（仍校验清单）；不同则新入口必须命中已审 SHA-256 表。
 */
export function evaluateGate(input: GateInput): GateResult {
  const {
    packageName,
    specifier,
    exports: exportsField,
    baselineConditions,
    additionalConditions = [],
    allowedConditions = [],
    manifest,
    reviewed = {},
    readFile,
  } = input;

  const subpath = deriveSubpath(specifier, packageName);

  const allowed = new Set(allowedConditions);
  const seen = new Set<string>();
  const activeAdditional: string[] = [];
  const ignoredConditions: string[] = [];
  for (const condition of additionalConditions) {
    if (seen.has(condition)) continue;
    seen.add(condition);
    if (allowed.has(condition)) activeAdditional.push(condition);
    else ignoredConditions.push(condition);
  }

  const baseline = resolveExports(exportsField, subpath, baselineConditions);
  const augmented = resolveExports(
    exportsField,
    subpath,
    [...baselineConditions, ...activeAdditional],
  );

  const probes: ProbeResult[] = activeAdditional.map((condition) => {
    const probe = resolveExports(
      exportsField,
      subpath,
      [...baselineConditions, condition],
    );
    return {
      condition,
      target: probe.target,
      blocked: probe.blocked,
      diverges: probe.target !== baseline.target,
    };
  });

  const changed = augmented.target !== baseline.target;
  let condition: string | null = null;
  let attribution: "single" | "combined" | null = null;
  if (changed) {
    const diverging = probes.filter((probe) => probe.diverges);
    if (diverging.length === 1) {
      condition = diverging[0].condition;
      attribution = "single";
    } else {
      condition = diverging[0]?.condition ?? activeAdditional[0] ?? null;
      attribution = "combined";
    }
  }

  const manifestSet = new Set(manifest.map(stripDotSlash));
  const selectedRel = augmented.target ? stripDotSlash(augmented.target) : null;
  const baselineRel = baseline.target ? stripDotSlash(baseline.target) : null;
  const selectedListed = selectedRel !== null && manifestSet.has(selectedRel);
  const baselineListed = baselineRel !== null && manifestSet.has(baselineRel);

  const reviewedTable = new Map<string, string>();
  for (const [path, hash] of Object.entries(reviewed)) {
    reviewedTable.set(stripDotSlash(path), hash.toLowerCase());
  }

  const review: ReviewVerdict = { listed: false, hashOk: null };
  if (changed && augmented.found && selectedRel !== null) {
    const expected = reviewedTable.get(selectedRel);
    if (expected !== undefined) {
      review.listed = true;
      review.expected = expected;
      try {
        if (!readFile) throw new Error("no readFile provided");
        review.actual = sha256Hex(readFile(selectedRel));
        review.hashOk = review.actual === expected;
      } catch (error) {
        review.readError = error instanceof Error ? error.message : String(error);
      }
    }
  }

  let reason: GateResult["reason"];
  if (augmented.blocked) reason = "deny-blocked";
  else if (!augmented.found) reason = "deny-unresolvable";
  else if (!selectedListed) reason = "deny-missing-from-manifest";
  else if (!changed) reason = "allow-same-entry";
  else if (!review.listed) reason = "deny-unreviewed-entry";
  else if (review.hashOk === null) reason = "deny-hash-unreadable";
  else if (!review.hashOk) reason = "deny-hash-mismatch";
  else reason = "allow-reviewed-entry";

  return {
    decision: reason.startsWith("allow") ? "allow" : "deny",
    reason,
    specifier,
    subpath,
    selectedEntry: augmented.target,
    activeAdditionalConditions: activeAdditional,
    ignoredConditions,
    baseline,
    augmented,
    divergence: {
      changed,
      condition,
      attribution,
      key: augmented.trace.winningKeyPath ?? augmented.trace.blockedKeyPath,
      baselineKey: baseline.trace.winningKeyPath,
      probes,
    },
    manifest: { selectedListed, baselineListed },
    review,
  };
}
