import { PathSyntaxError } from './errors.ts';

/**
 * Path syntax: segments separated by `/`.
 * Inside a segment, `\/` is a literal slash and `\\` is a literal backslash,
 * so object keys that themselves contain `/` or `\` round-trip safely.
 * The empty string denotes the root (zero segments).
 */
export function parsePath(input: string): string[] {
  if (input === '') return [];
  const segments: string[] = [];
  let current = '';
  let i = 0;
  const push = (): void => {
    segments.push(current);
    current = '';
  };
  while (i < input.length) {
    const ch = input[i] as string;
    if (ch === '\\') {
      const next = input[i + 1];
      if (next === '/' || next === '\\') {
        current += next;
        i += 2;
        continue;
      }
      throw new PathSyntaxError(input, i, `unsupported escape \\${next ?? ''}; use \\/ or \\\\`);
    }
    if (ch === '/') {
      push();
      i += 1;
      continue;
    }
    current += ch;
    i += 1;
  }
  push();
  return segments;
}

export function escapeSegment(segment: string): string {
  return segment.replace(/\\/g, '\\\\').replace(/\//g, '\\/');
}

export function formatPath(segments: string[]): string {
  return segments.map(escapeSegment).join('/');
}

export function isPrefixPath(prefix: string[], path: string[]): boolean {
  if (prefix.length > path.length) return false;
  return prefix.every((seg, i) => seg === path[i]);
}

export function isArrayIndex(segment: string): boolean {
  return /^(0|[1-9]\d*)$/.test(segment);
}
