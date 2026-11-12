import { globCovers, patternCovers } from './match-pattern.ts';

/**
 * Inputs describing what the extension actually needs.
 *
 * `requiredUrls` are concrete origins/paths derived from declared features
 * (e.g. API endpoints hard-coded in the extension, content-script targets).
 * `requiredPatterns` are match patterns the feature explicitly demands
 * (e.g. a content script registered for `https://*.api.example.com/v2/*`).
 */
export interface FunctionalityRequirements {
  requiredUrls?: string[];
  requiredPatterns?: string[];
}

/**
 * Compute the smallest set of valid match patterns that still covers every
 * declared requirement. Patterns already covered by another pattern are
 * removed, and concrete URLs are merged into per-host path groups.
 */
export function minimalPatternSet(requirements: FunctionalityRequirements): string[] {
  const candidates = new Set<string>(requirements.requiredPatterns ?? []);
  for (const url of requirements.requiredUrls ?? []) {
    candidates.add(patternFromUrl(url));
  }

  const patterns = [...candidates].sort();
  const kept: string[] = [];
  for (const candidate of patterns) {
    if (kept.some((existing) => patternCovers(existing, candidate))) {
      continue;
    }
    const siblings = kept.filter((existing) => sameSchemeHost(existing, candidate));
    if (siblings.length === 0) {
      kept.push(candidate);
      continue;
    }
    for (const sibling of siblings) {
      const idx = kept.indexOf(sibling);
      if (idx !== -1) {
        kept.splice(idx, 1);
      }
    }
    kept.push(...mergePathGroups(candidate, siblings));
  }
  return kept.sort();
}

/** Convert a concrete URL into the tightest possible match pattern. */
export function patternFromUrl(url: string): string {
  const parsed = new URL(url);
  const scheme = parsed.protocol.slice(0, -1);
  if (scheme === 'file') {
    return `file://${parsed.pathname}`;
  }
  const path = parsed.pathname || '/';
  return `${scheme}://${parsed.hostname.toLowerCase()}${path}`;
}

function sameSchemeHost(a: string, b: string): boolean {
  const [schemeA, hostA] = splitSchemeHost(a);
  const [schemeB, hostB] = splitSchemeHost(b);
  return schemeA === schemeB && hostA === hostB;
}

function splitSchemeHost(pattern: string): [string, string] {
  const sep = pattern.indexOf('://');
  const scheme = pattern.slice(0, sep);
  const rest = pattern.slice(sep + 3);
  const slash = rest.indexOf('/');
  return [scheme, slash === -1 ? rest : rest.slice(0, slash)];
}

/**
 * Collapse patterns sharing scheme+host. When several exact paths are needed
 * on the same host, expand to the common directory prefix with a trailing `*`,
 * which is the idiomatic minimal Chrome pattern for that group.
 */
function mergePathGroups(candidate: string, siblings: string[]): string[] {
  if (siblings.length === 0) {
    return [candidate];
  }
  const [scheme, host] = splitSchemeHost(candidate);
  const pathStart = (pattern: string): number => pattern.indexOf('/', pattern.indexOf('://') + 3);
  const candidatePath = candidate.slice(pathStart(candidate));
  const paths = siblings.map((pattern) => pattern.slice(pattern.indexOf('/', pattern.indexOf('://') + 3)));
  paths.push(candidatePath);

  if (paths.every((path) => path === paths[0])) {
    return [`${scheme}://${host}${paths[0]}`];
  }

  const prefix = commonDirectoryPrefix(paths);
  const mergedPath = `${prefix}*`;
  const merged = `${scheme}://${host}${mergedPath}`;
  const distinct = new Set<string>([merged]);
  for (const sibling of siblings) {
    const siblingPath = sibling.slice(pathStart(sibling));
    if (!globCovers(mergedPath, siblingPath)) {
      distinct.add(sibling);
    }
  }
  return [...distinct];
}

function commonDirectoryPrefix(paths: string[]): string {
  let prefix = paths[0];
  for (const path of paths.slice(1)) {
    let common = 0;
    const limit = Math.min(prefix.length, path.length);
    while (common < limit && prefix[common] === path[common]) {
      common++;
    }
    prefix = prefix.slice(0, common);
  }
  const lastSlash = prefix.lastIndexOf('/');
  return lastSlash <= 0 ? '/' : prefix.slice(0, lastSlash + 1);
}

/** Remove patterns that are fully covered by another pattern in the same list. */
export function reducePatterns(patterns: string[]): string[] {
  const sorted = [...new Set(patterns)].sort();
  return sorted.filter((candidate) => !sorted.some((other) => other !== candidate && patternCovers(other, candidate)));
}
