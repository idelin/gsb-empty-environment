/**
 * Chrome MV3 match-pattern parsing and coverage logic.
 *
 * Reference: https://developer.chrome.com/docs/extensions/develop/concepts/match-patterns
 * A pattern is `<scheme>://<host><path>`, or the special token `<all_urls>`.
 */

export type MatchScheme = 'http' | 'https' | 'file' | 'ftp' | 'urn' | '*';

export interface ParsedPattern {
  /** Original pattern text. */
  raw: string;
  /** True for the `<all_urls>` shorthand. */
  allUrls: boolean;
  scheme: MatchScheme;
  /** Lower-cased host without trailing dot; `*` means any host (only with `*` scheme). */
  host: string;
  /** True when the host is a `*.example.com` style wildcard. */
  hostWildcard: boolean;
  /** Path component, always starting with `/` (except `urn` patterns). */
  path: string;
}

export class InvalidMatchPatternError extends Error {
  constructor(pattern: string, reason: string) {
    super(`Invalid match pattern "${pattern}": ${reason}`);
    this.name = 'InvalidMatchPatternError';
  }
}

const SCHEMES: readonly MatchScheme[] = ['http', 'https', 'file', 'ftp', 'urn', '*'];
const HOST_LABEL = /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/;

/** Parse and validate a Chrome match pattern. Throws {@link InvalidMatchPatternError}. */
export function parseMatchPattern(pattern: string): ParsedPattern {
  if (typeof pattern !== 'string' || pattern.length === 0) {
    throw new InvalidMatchPatternError(String(pattern), 'pattern must be a non-empty string');
  }
  if (pattern === '<all_urls>') {
    return { raw: pattern, allUrls: true, scheme: '*', host: '*', hostWildcard: true, path: '/*' };
  }

  const schemeSep = pattern.indexOf('://');
  if (schemeSep === -1) {
    if (pattern.startsWith('urn:')) {
      return {
        raw: pattern,
        allUrls: false,
        scheme: 'urn',
        host: '',
        hostWildcard: false,
        path: pattern.slice(4),
      };
    }
    throw new InvalidMatchPatternError(pattern, 'missing "://" separator');
  }

  const scheme = pattern.slice(0, schemeSep);
  if (!SCHEMES.includes(scheme as MatchScheme)) {
    throw new InvalidMatchPatternError(pattern, `unsupported scheme "${scheme}"`);
  }

  const rest = pattern.slice(schemeSep + 3);
  const pathIdx = rest.indexOf('/');
  if (pathIdx === -1) {
    throw new InvalidMatchPatternError(pattern, 'path is required (use "/*" to match any path)');
  }
  const host = rest.slice(0, pathIdx).toLowerCase();
  const path = rest.slice(pathIdx);

  if (!path.startsWith('/')) {
    throw new InvalidMatchPatternError(pattern, 'path must start with "/"');
  }
  if (path.includes('#')) {
    throw new InvalidMatchPatternError(pattern, 'path must not contain "#"');
  }

  if (scheme === 'file') {
    if (host !== '') {
      throw new InvalidMatchPatternError(pattern, 'file: patterns must not declare a host');
    }
  } else {
    validateHost(pattern, scheme as MatchScheme, host);
  }

  return {
    raw: pattern,
    allUrls: false,
    scheme: scheme as MatchScheme,
    host,
    hostWildcard: host.startsWith('*.'),
    path,
  };
}

function validateHost(pattern: string, scheme: MatchScheme, host: string): void {
  if (host.length === 0) {
    throw new InvalidMatchPatternError(pattern, 'host is required');
  }
  if (host === '*') {
    if (scheme !== '*') {
      throw new InvalidMatchPatternError(pattern, 'host "*" requires the "*" scheme');
    }
    return;
  }
  let labels = host;
  if (host.startsWith('*.')) {
    labels = host.slice(2);
    if (labels.length === 0) {
      throw new InvalidMatchPatternError(pattern, 'wildcard host needs a domain after "*."');
    }
  }
  for (const label of labels.split('.')) {
    if (!HOST_LABEL.test(label)) {
      throw new InvalidMatchPatternError(pattern, `invalid host label "${label}"`);
    }
  }
}

/** True when `url` is matched by `pattern`. */
export function matchesUrl(pattern: string | ParsedPattern, url: string): boolean {
  const parsed = typeof pattern === 'string' ? parseMatchPattern(pattern) : pattern;
  let target: URL;
  try {
    target = new URL(url);
  } catch {
    return false;
  }

  if (parsed.allUrls) {
    return true;
  }
  if (!schemeMatches(parsed.scheme, target.protocol.slice(0, -1))) {
    return false;
  }
  if (parsed.scheme === 'file') {
    return globMatches(parsed.path, target.pathname);
  }
  if (parsed.scheme === 'urn') {
    return globMatches(parsed.path, target.pathname || target.href.slice(target.protocol.length));
  }
  if (!hostMatches(parsed, target.hostname.toLowerCase())) {
    return false;
  }
  // Match patterns compare the path only; the query string is ignored.
  return globMatches(parsed.path, target.pathname || '/');
}

function schemeMatches(patternScheme: MatchScheme, urlScheme: string): boolean {
  if (patternScheme === '*') {
    return urlScheme === 'http' || urlScheme === 'https';
  }
  return patternScheme === urlScheme;
}

function hostMatches(pattern: ParsedPattern, hostname: string): boolean {
  if (pattern.host === '*') {
    return hostname.length > 0;
  }
  if (pattern.hostWildcard) {
    const base = pattern.host.slice(2);
    // `*.example.com` matches `example.com` itself as well as every subdomain.
    return hostname === base || hostname.endsWith('.' + base);
  }
  return pattern.host === hostname;
}

/** Glob match where `*` matches any run of characters (including the empty string). */
export function globMatches(glob: string, value: string): boolean {
  let globIdx = 0;
  let valueIdx = 0;
  let star = -1;
  let mark = 0;
  while (valueIdx < value.length) {
    if (globIdx < glob.length && glob[globIdx] === '*') {
      star = globIdx++;
      mark = valueIdx;
    } else if (globIdx < glob.length && glob[globIdx] === value[valueIdx]) {
      globIdx++;
      valueIdx++;
    } else if (star !== -1) {
      globIdx = star + 1;
      valueIdx = ++mark;
    } else {
      return false;
    }
  }
  while (globIdx < glob.length && glob[globIdx] === '*') {
    globIdx++;
  }
  return globIdx === glob.length;
}

/**
 * Language inclusion for single-wildcard globs: every string accepted by
 * `child` must also be accepted by `parent`.
 *
 * Both globs are compiled to Thompson-style NFAs where a `*` fragment offers
 * an epsilon skip and an epsilon self-loop through an "any character" state.
 * Inclusion is decided by subset-product simulation: for every reachable
 * child NFA subset, the parent subset must contain an accepting state whenever
 * the child subset does, on every concrete character the child can consume
 * (including the wildcard's implicit any-character moves).
 */
export function globCovers(parent: string, child: string): boolean {
  const parentNfa = buildGlobNfa(parent);
  const childNfa = buildGlobNfa(child);

  const alphabet = new Set<string>();
  for (const nfa of [parentNfa, childNfa]) {
    for (const state of nfa) {
      for (const ch of state.literal.keys()) {
        alphabet.add(ch);
      }
    }
  }
  const startParent = epsilonClosure(parentNfa, [0]);
  const startChild = epsilonClosure(childNfa, [0]);
  const seen = new Set<string>();
  const queue: Array<{ parent: number[]; child: number[] }> = [
    { parent: startParent, child: startChild },
  ];

  while (queue.length > 0) {
    const { parent, child: childStates } = queue.pop() as {
      parent: number[];
      child: number[];
    };
    const key = `${parent.join('.')}#${childStates.join('.')}`;
    if (seen.has(key)) {
      continue;
    }
    seen.add(key);

    const childAccepts = childStates.includes(childNfa.length - 1);
    const parentAccepts = parent.includes(parentNfa.length - 1);
    if (childAccepts && !parentAccepts) {
      return false;
    }

    // Explore every concrete literal character, plus one symbolic "other
    // character" move that only wildcard states can take. Literal edges never
    // fire on the symbolic move, so one extra symbol covers all unlisted chars.
    const moves: Array<{ ch: string; wildcardOnly: boolean }> = [
      ...[...alphabet].map((ch) => ({ ch, wildcardOnly: false })),
      { ch: '', wildcardOnly: true },
    ];
    for (const move of moves) {
      const nextChild = moveNfa(childNfa, childStates, move.ch, move.wildcardOnly);
      if (nextChild.length === 0) {
        continue;
      }
      const nextParent = moveNfa(parentNfa, parent, move.ch, move.wildcardOnly);
      queue.push({ parent: nextParent, child: nextChild });
    }
  }
  return true;
}

interface GlobNfaState {
  epsilon: number[];
  literal: Map<string, number[]>;
  any: number[];
}

/**
 * Thompson NFA for a `*`-glob. A literal token is one char edge; a `*` token
 * is a fragment with an epsilon skip and an epsilon self-loop over an
 * any-character state, exactly mirroring wildcard-matching semantics.
 */
function buildGlobNfa(glob: string): GlobNfaState[] {
  const states: GlobNfaState[] = [];
  const newState = (): number => {
    states.push({ epsilon: [], literal: new Map(), any: [] });
    return states.length - 1;
  };
  const start = newState();
  let previousExit = start;

  for (const token of glob) {
    if (token === '*') {
      const entry = newState();
      const anyState = newState();
      const exit = newState();
      states[previousExit].epsilon.push(entry);
      states[entry].epsilon.push(exit);
      states[entry].epsilon.push(anyState);
      states[anyState].any.push(entry);
      previousExit = exit;
    } else {
      const entry = newState();
      const exit = newState();
      states[previousExit].epsilon.push(entry);
      states[entry].literal.set(token, [exit]);
      previousExit = exit;
    }
  }
  return states;
}

function epsilonClosure(nfa: GlobNfaState[], seeds: number[]): number[] {
  const result = new Set(seeds);
  const stack = [...seeds];
  while (stack.length > 0) {
    const state = stack.pop() as number;
    for (const next of nfa[state].epsilon) {
      if (!result.has(next)) {
        result.add(next);
        stack.push(next);
      }
    }
  }
  return [...result];
}

function moveNfa(
  nfa: GlobNfaState[],
  states: number[],
  ch: string,
  wildcardOnly: boolean,
): number[] {
  const direct: number[] = [];
  for (const state of states) {
    if (wildcardOnly) {
      // Symbolic move for a character outside the literal alphabet.
      direct.push(...nfa[state].any);
    } else {
      // A concrete character takes both its literal edge and any wildcard
      // edge reachable from this state.
      direct.push(...(nfa[state].literal.get(ch) ?? []));
      direct.push(...nfa[state].any);
    }
  }
  return epsilonClosure(nfa, [...new Set(direct)]);
}

/**
 * Pattern-level coverage: every URL matched by `child` is also matched by
 * `parent`. Throws {@link InvalidMatchPatternError} for malformed patterns.
 */
export function patternCovers(parentInput: string, childInput: string): boolean {
  const parent = parseMatchPattern(parentInput);
  const child = parseMatchPattern(childInput);

  if (parent.allUrls) {
    return true;
  }
  if (child.allUrls) {
    return false;
  }
  if (!schemeCovers(parent.scheme, child.scheme)) {
    return false;
  }
  if (parent.scheme !== 'file' && parent.scheme !== 'urn' && !hostCovers(parent, child)) {
    return false;
  }
  return globCovers(parent.path, child.path);
}

function schemeCovers(parent: MatchScheme, child: MatchScheme): boolean {
  if (parent === child) {
    return true;
  }
  if (parent === '*') {
    return child === 'http' || child === 'https' || child === '*';
  }
  return false;
}

function hostCovers(parent: ParsedPattern, child: ParsedPattern): boolean {
  if (parent.host === '*') {
    return true;
  }
  if (child.host === '*') {
    return false;
  }
  if (parent.hostWildcard) {
    const parentBase = parent.host.slice(2);
    if (child.hostWildcard) {
      const childBase = child.host.slice(2);
      return childBase === parentBase || childBase.endsWith('.' + parentBase);
    }
    return child.host === parentBase || child.host.endsWith('.' + parentBase);
  }
  if (child.hostWildcard) {
    return false;
  }
  return parent.host === child.host;
}
