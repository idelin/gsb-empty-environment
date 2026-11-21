function globToRegExp(pattern: string): RegExp {
  let out = '^';
  for (const ch of pattern) {
    if (ch === '*') {
      out += '.*';
    } else if (ch === '?') {
      out += '.';
    } else {
      out += ch.replace(/[.+^${}()|[\]\\]/g, '\\$&');
    }
  }
  out += '$';
  return new RegExp(out);
}

export function globMatch(pattern: string, value: string): boolean {
  if (pattern === value) {
    return true;
  }
  if (!pattern.includes('*') && !pattern.includes('?')) {
    return false;
  }
  return globToRegExp(pattern).test(value);
}

export function matchAnyGlob(patterns: readonly string[], value: string): boolean {
  return patterns.some((pattern) => globMatch(pattern, value));
}

export function segmentMatch(pattern: string, value: string): boolean {
  if (pattern === value) {
    return true;
  }
  const patternParts = pattern.split(':');
  const valueParts = value.split(':');
  if (patternParts.length !== valueParts.length) {
    return globMatch(pattern, value);
  }
  return patternParts.every((part, index) => globMatch(part, valueParts[index]));
}
