import type {
  AnalysisOptions,
  AnalysisReport,
  Finding,
  RedirectChain,
} from './types.ts';
import { parseManifest } from './parser.ts';
import {
  analyzeExportSurface,
  createPermissionResolver,
} from './exportSurface.ts';
import { analyzeRedirects } from './redirectAnalysis.ts';

function findingRank(severity: Finding['severity']): number {
  const ranks: Record<Finding['severity'], number> = {
    critical: 0,
    high: 1,
    medium: 2,
    low: 3,
    info: 4,
  };
  return ranks[severity];
}

export function analyzeManifest(
  xml: string,
  options: AnalysisOptions = {},
): AnalysisReport {
  const manifest = parseManifest(xml);
  const registry = options.permissionRegistry ?? {};
  const { surfaces, findings } = analyzeExportSurface(manifest, registry);
  const resolver = createPermissionResolver(manifest, registry);

  const redirectResult = analyzeRedirects(
    manifest,
    surfaces,
    resolver,
    options.redirects ?? [],
    options.maxHops ?? 8,
  );

  const redirectFindings: Finding[] = [];
  for (const chain of redirectResult.chains) {
    redirectFindings.push(chainToFinding(chain));
  }

  const allFindings = [...findings, ...redirectFindings].sort(
    (left, right) =>
      findingRank(left.severity) - findingRank(right.severity) ||
      left.id.localeCompare(right.id),
  );

  return {
    packageName: manifest.packageName,
    targetSdk: manifest.targetSdk,
    summary: {
      componentCount: manifest.components.length,
      exportedCount: surfaces.filter((surface) => surface.exported).length,
      implicitlyExportedCount: surfaces.filter(
        (surface) => surface.implicitlyExported,
      ).length,
      unprotectedExportedCount: findings.filter(
        (finding) =>
          finding.type === 'unprotected-exported-component' ||
          finding.type === 'unprotected-provider',
      ).length,
      redirectOnlyCount: redirectResult.redirectOnly.length,
      exploitableChainCount: redirectResult.chains.filter(
        (chain) => chain.exploitable,
      ).length,
    },
    components: surfaces,
    findings: allFindings,
    redirectChains: redirectResult.chains,
    redirectOnlyComponents: redirectResult.redirectOnly,
  };
}

function chainToFinding(chain: RedirectChain): Finding {
  const pathText = [chain.entry, ...chain.hops.map((hop) => hop.to)].join(
    ' -> ',
  );
  return {
    id: `redirect-chain:${chain.entry}->${chain.target}#${chain.hopCount}`,
    severity: chain.exploitable ? chain.severity : 'info',
    type: 'redirect-to-private-component',
    component: chain.target,
    message: chain.exploitable
      ? `private component '${chain.target}' is reachable from exported entry '${chain.entry}' via ${chain.hopCount} redirect hop(s): ${pathText}`
      : `redirect path to private component '${chain.target}' is not exploitable by third parties: ${pathText}`,
    evidence: {
      entry: chain.entry,
      entryAccessible: chain.entryAccessible,
      target: chain.target,
      hops: chain.hops,
      blockedBy: chain.blockedBy ?? null,
      intentPassthrough: chain.intentPassthrough,
      uriGrantBypass: chain.uriGrantBypass,
    },
  };
}
