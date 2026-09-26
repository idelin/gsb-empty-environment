/** JSON Pointer (RFC 6901) helpers, rendered with a leading "#". */

export function escapeSegment(segment: string): string {
  return segment.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, '/').replace(/~0/g, '~');
}

export function toPointer(segments: string[]): string {
  if (segments.length === 0) return '#';
  return '#' + segments.map((s) => '/' + escapeSegment(s)).join('');
}

export function parsePointer(pointer: string): string[] {
  if (pointer === '#' || pointer === '') return [];
  const raw = pointer.startsWith('#') ? pointer.slice(1) : pointer;
  if (!raw.startsWith('/')) throw new Error(`invalid pointer: ${pointer}`);
  return raw.slice(1).split('/').map(unescapeSegment);
}

export function getPointer(doc: unknown, segments: string[]): unknown {
  let current: unknown = doc;
  for (const segment of segments) {
    if (current === null || typeof current !== 'object') return undefined;
    if (Array.isArray(current)) {
      const index = Number(segment);
      if (!Number.isInteger(index)) return undefined;
      current = current[index];
    } else {
      current = (current as Record<string, unknown>)[segment];
    }
  }
  return current;
}
