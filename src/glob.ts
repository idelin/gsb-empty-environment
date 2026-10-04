/**
 * Minimal glob support: `*` (within a path segment), `?` (single char),
 * `**` (any number of segments). Also provides conservative relations used
 * by the static conflict checker.
 */

function escapeRegExp(ch: string): string {
  return ch.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function globToRegExp(glob: string): RegExp {
  let re = '';
  let i = 0;
  while (i < glob.length) {
    const c = glob[i];
    if (c === '*') {
      if (glob[i + 1] === '*') {
        if (glob[i + 2] === '/') {
          re += '(?:.*/)?'; // '**/' also matches zero directories
          i += 3;
        } else {
          re += '.*';
          i += 2;
        }
      } else {
        re += '[^/]*';
        i += 1;
      }
    } else if (c === '?') {
      re += '[^/]';
      i += 1;
    } else {
      re += escapeRegExp(c);
      i += 1;
    }
  }
  return new RegExp('^' + re + '$');
}

export function globMatches(glob: string, path: string): boolean {
  return globToRegExp(glob).test(path);
}

/** True when the glob contains no wildcard characters. */
export function isLiteralGlob(glob: string): boolean {
  return !/[*?]/.test(glob);
}

/** True when glob is `literal/**`; returns the literal prefix (with '/'). */
export function literalPrefixOf(glob: string): string | null {
  if (!glob.endsWith('/**')) return null;
  const prefix = glob.slice(0, -2);
  return isLiteralGlob(prefix) ? prefix : null;
}

/**
 * Conservative subsumption: true only when every path matched by `b` is
 * guaranteed to be matched by `a`. False means "cannot prove".
 */
export function globSubsumes(a: string, b: string): boolean {
  if (a === b) return true;
  if (a === '**') return true;
  const pa = literalPrefixOf(a);
  if (pa !== null && b.startsWith(pa)) return true;
  return false;
}

/**
 * Conservative disjointness: true only when no path can match both globs.
 * False means "may overlap".
 */
export function globDisjoint(a: string, b: string): boolean {
  if (isLiteralGlob(a) && isLiteralGlob(b)) return a !== b;
  const pa = literalPrefixOf(a);
  const pb = literalPrefixOf(b);
  if (pa !== null && isLiteralGlob(b)) return !b.startsWith(pa);
  if (pb !== null && isLiteralGlob(a)) return !a.startsWith(pb);
  if (pa !== null && pb !== null) {
    return !pa.startsWith(pb) && !pb.startsWith(pa);
  }
  return false;
}
