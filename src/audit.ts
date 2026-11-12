import {
  InvalidMatchPatternError,
  globCovers,
  parseMatchPattern,
  patternCovers,
  type ParsedPattern,
} from './match-pattern.ts';
import { minimalPatternSet, type FunctionalityRequirements } from './minimize.ts';

/** Subset of the MV3 manifest this auditor reads. */
export interface ManifestLike {
  host_permissions?: string[];
  permissions?: string[];
  content_scripts?: Array<{ matches?: string[] }>;
  web_accessible_resources?: Array<{ resources?: string[]; matches?: string[] }>;
}

export type ExcessReason =
  | 'scheme-too-broad'
  | 'host-too-broad'
  | 'path-too-broad'
  | 'all-urls'
  | 'unrequired';

export interface ExcessPattern {
  pattern: string;
  /** Where the pattern was declared in the manifest. */
  source: 'host_permissions' | 'content_scripts' | 'web_accessible_resources';
  reasons: ExcessReason[];
  /** Tighter patterns that would still cover every required URL in scope. */
  suggestedReplacements: string[];
}

export interface RedundantPattern {
  pattern: string;
  source: ExcessPattern['source'];
  coveredBy: string;
}

export interface InvalidPattern {
  pattern: string;
  source: ExcessPattern['source'];
  error: string;
}

export interface ManifestAuditReport {
  /** Minimal patterns needed for the declared functionality. */
  required: string[];
  /** Patterns declared by the manifest that reach beyond the requirements. */
  excess: ExcessPattern[];
  /** Patterns fully subsumed by another declaration in the same manifest. */
  redundant: RedundantPattern[];
  /** Required coverage that no declared pattern provides. */
  missing: string[];
  /** Patterns Chrome would reject outright. */
  invalid: InvalidPattern[];
  /**
   * `web_accessible_resources` entries whose `matches` are broader than the
   * corresponding `host_permissions`, letting arbitrary sites probe the
   * extension's exposed resources.
   */
  webResourceRisks: Array<{ matches: string[]; resources: string[]; exposedBeyondHostPermissions: string[] }>;
}

interface DeclaredEntry {
  pattern: string;
  source: ExcessPattern['source'];
}

/**
 * Compare a manifest's declared host access against the patterns actually
 * required by its functionality and report over-broad declarations.
 */
export function auditManifest(manifest: ManifestLike, requirements: FunctionalityRequirements): ManifestAuditReport {
  const required = minimalPatternSet(requirements);
  const declared = collectDeclared(manifest);

  const invalid: InvalidPattern[] = [];
  const valid: Array<DeclaredEntry & { parsed: ParsedPattern }> = [];
  for (const entry of declared) {
    try {
      valid.push({ ...entry, parsed: parseMatchPattern(entry.pattern) });
    } catch (error) {
      invalid.push({
        pattern: entry.pattern,
        source: entry.source,
        error: error instanceof InvalidMatchPatternError ? error.message : String(error),
      });
    }
  }

  const redundant = findRedundant(valid);
  const redundantKeys = new Set(redundant.map((item) => `${item.source}:${item.pattern}`));
  const excess = valid
    .filter((entry) => !redundantKeys.has(`${entry.source}:${entry.pattern}`))
    .map((entry) => assessEntry(entry, required))
    .filter((item): item is ExcessPattern => item !== null);

  const hostPermissionPatterns = valid
    .filter((entry) => entry.source === 'host_permissions')
    .map((entry) => entry.pattern);
  const missing = required.filter((need) => !valid.some((entry) => patternCovers(entry.pattern, need)));
  const webResourceRisks = assessWebAccessibleResources(manifest, hostPermissionPatterns);

  return { required, excess, redundant, missing, invalid, webResourceRisks };
}

function collectDeclared(manifest: ManifestLike): DeclaredEntry[] {
  const entries: DeclaredEntry[] = [];
  for (const pattern of manifest.host_permissions ?? []) {
    entries.push({ pattern, source: 'host_permissions' });
  }
  for (const script of manifest.content_scripts ?? []) {
    for (const pattern of script.matches ?? []) {
      entries.push({ pattern, source: 'content_scripts' });
    }
  }
  for (const resource of manifest.web_accessible_resources ?? []) {
    for (const pattern of resource.matches ?? []) {
      entries.push({ pattern, source: 'web_accessible_resources' });
    }
  }
  return entries;
}

function findRedundant(entries: Array<DeclaredEntry & { parsed: ParsedPattern }>): RedundantPattern[] {
  const result: RedundantPattern[] = [];
  for (const entry of entries) {
    const cover = entries.find(
      (other) =>
        other !== entry &&
        other.source === entry.source &&
        other.pattern !== entry.pattern &&
        patternCovers(other.pattern, entry.pattern),
    );
    if (cover) {
      result.push({ pattern: entry.pattern, source: entry.source, coveredBy: cover.pattern });
    }
  }
  return result;
}

function assessEntry(
  entry: DeclaredEntry & { parsed: ParsedPattern },
  required: string[],
): ExcessPattern | null {
  const inScope = required.filter((need) => patternCovers(entry.pattern, need));
  if (inScope.length === 0) {
    return {
      pattern: entry.pattern,
      source: entry.source,
      reasons: [entry.parsed.allUrls ? 'all-urls' : 'unrequired'],
      suggestedReplacements: [],
    };
  }

  const reasons: ExcessReason[] = [];
  if (entry.parsed.allUrls) {
    reasons.push('all-urls');
  } else {
    for (const need of inScope) {
      const requiredPattern = parseMatchPattern(need);
      if (!schemeCoversExactly(entry.parsed.scheme, requiredPattern.scheme)) {
        reasons.push('scheme-too-broad');
      }
      if (entry.parsed.scheme !== 'file' && entry.parsed.scheme !== 'urn' && hostIsBroader(entry.parsed, requiredPattern)) {
        reasons.push('host-too-broad');
      }
      if (!globCovers(requiredPattern.path, entry.parsed.path)) {
        reasons.push('path-too-broad');
      }
    }
  }

  if (reasons.length === 0) {
    return null;
  }
  return {
    pattern: entry.pattern,
    source: entry.source,
    reasons: [...new Set(reasons)],
    suggestedReplacements: tightenPatterns(entry.pattern, inScope),
  };
}

function schemeCoversExactly(declared: ParsedPattern['scheme'], required: ParsedPattern['scheme']): boolean {
  if (declared === required) {
    return true;
  }
  if (declared === '*' && required !== '*') {
    return false;
  }
  return false;
}

function hostIsBroader(declared: ParsedPattern, required: ParsedPattern): boolean {
  if (declared.host === '*' && required.host !== '*') {
    return true;
  }
  if (declared.hostWildcard && !required.hostWildcard) {
    const base = declared.host.slice(2);
    return required.host === base || required.host.endsWith('.' + base);
  }
  if (declared.hostWildcard && required.hostWildcard) {
    return declared.host !== required.host;
  }
  return declared.host !== required.host;
}

function tightenPatterns(declared: string, inScopeRequired: string[]): string[] {
  const replacements = new Set<string>();
  for (const need of inScopeRequired) {
    if (!patternCovers(declared, need)) {
      continue;
    }
    replacements.add(need);
  }
  return [...replacements].sort();
}

function assessWebAccessibleResources(
  manifest: ManifestLike,
  hostPermissionPatterns: string[],
): ManifestAuditReport['webResourceRisks'] {
  const risks: ManifestAuditReport['webResourceRisks'] = [];
  for (const resource of manifest.web_accessible_resources ?? []) {
    const matches = resource.matches ?? [];
    const exposedBeyondHostPermissions = matches.filter(
      (match) => !hostPermissionPatterns.some((permission) => patternCovers(permission, match)),
    );
    if (exposedBeyondHostPermissions.length > 0) {
      risks.push({
        matches,
        resources: resource.resources ?? [],
        exposedBeyondHostPermissions,
      });
    }
  }
  return risks;
}
