import { Condition, FileFacts, Range, Tri } from './types';

/** Convert a glob (`*`, `**`, `?`) into an anchored RegExp. */
export function globToRegExp(glob: string): RegExp {
  let out = '';
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i]!;
    if (c === '*') {
      if (glob[i + 1] === '*') {
        // `**` crosses directory boundaries; consume a following `/` too.
        if (glob[i + 2] === '/') { out += '(?:.*/)?'; i += 2; }
        else { out += '.*'; i += 1; }
      } else {
        out += '[^/]*';
      }
    } else if (c === '?') {
      out += '[^/]';
    } else {
      out += c.replace(/[.+^${}()|[\]\\]/g, '\\$&');
    }
  }
  return new RegExp('^' + out + '$');
}

export function matchGlob(glob: string, path: string): boolean {
  return globToRegExp(glob).test(path);
}

export function isLiteralGlob(glob: string): boolean {
  return !/[*?]/.test(glob);
}

function triAnd(a: Tri, b: Tri): Tri {
  if (a === false || b === false) return false;
  if (a === 'unknown' || b === 'unknown') return 'unknown';
  return true;
}

function evalRange(range: Range, value: number | undefined): Tri {
  if (value === undefined) return 'unknown';
  if (range.min !== undefined && value < range.min) return false;
  if (range.max !== undefined && value > range.max) return false;
  return true;
}

/**
 * Evaluate a condition against possibly-incomplete facts.
 * A fact that is absent never defaults to exclusion: it yields 'unknown'.
 */
export function evalCondition(cond: Condition, facts: FileFacts): Tri {
  let result: Tri = true;

  if (cond.path !== undefined) {
    result = triAnd(result, matchGlob(cond.path, facts.path));
  }
  if (cond.pathNot !== undefined) {
    result = triAnd(result, !matchGlob(cond.pathNot, facts.path));
  }
  if (cond.size !== undefined) {
    result = triAnd(result, evalRange(cond.size, facts.size));
  }
  if (cond.version !== undefined) {
    result = triAnd(result, evalRange(cond.version, facts.version));
  }
  if (cond.tagsAll !== undefined && cond.tagsAll.length > 0) {
    if (facts.tags === undefined) {
      result = triAnd(result, 'unknown');
    } else {
      const owned = new Set(facts.tags);
      result = triAnd(result, cond.tagsAll.every((t) => owned.has(t)));
    }
  }
  if (cond.tagsNone !== undefined && cond.tagsNone.length > 0) {
    if (facts.tags === undefined) {
      result = triAnd(result, 'unknown');
    } else {
      const owned = new Set(facts.tags);
      result = triAnd(result, !cond.tagsNone.some((t) => owned.has(t)));
    }
  }
  return result;
}
