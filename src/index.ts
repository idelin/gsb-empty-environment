export * from "./types.ts";
export * from "./match-pattern.ts";
export * from "./manifest.ts";
export * from "./declarative-net-request.ts";
export * from "./targets.ts";
export * from "./capability-token.ts";
export * from "./runtime-gate.ts";
export * from "./attack-path.ts";

import { searchConfusedDeputyPaths } from "./attack-path.ts";
import { reviewRuntimeSecurity } from "./runtime-gate.ts";
import type { SecurityAnalysisInput, SecurityAnalysisResult } from "./types.ts";

export function analyzeSecurity(input: SecurityAnalysisInput): SecurityAnalysisResult {
  const review = reviewRuntimeSecurity({
    manifest: input.manifest,
    routes: input.routes,
    dnrRules: input.dnrRules,
  });
  const search = searchConfusedDeputyPaths({
    manifest: input.manifest,
    routes: input.routes,
    entries: input.entries,
    untrustedOrigins: input.untrustedOrigins,
    maxHops: input.maxHops,
    maxPaths: input.maxPaths,
  });

  const pathIssues = search.paths.map((path) => ({
    code: "CONFUSED_DEPUTY_PATH" as const,
    severity: path.severity,
    message: `${path.entryOrigin} can reach ${path.sink.kind} on ${
      path.sink.target ?? "<global target>"
    } in ${path.hopCount} hop(s) via ${path.hops
      .filter((hop) => !hop.routeId.startsWith("war:"))
      .map((hop) => hop.routeId)
      .join(" -> ")}`,
    routeId: path.sinkRouteId,
    evidence: {
      entryOrigin: path.entryOrigin,
      sink: path.sink,
      hops: path.hops,
      missingControls: path.missingControls,
    },
  }));

  const issues = [...review.issues, ...pathIssues];
  return {
    issues,
    attackPaths: search.paths,
    safe: !issues.some((entry) => entry.severity === "critical" || entry.severity === "high"),
  };
}
