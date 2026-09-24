import { snapBackward, snapForward } from './utf16.ts';
import type { ContextSummary, Range } from './types.ts';

export const DEFAULT_CONTEXT_RADIUS = 32;

/**
 * Capture the context summary for a diagnostic range. All offsets are
 * UTF-16 code units; the capture window is snapped outward so it never
 * splits a surrogate pair.
 */
export function captureContext(
  text: string,
  range: Range,
  radius: number = DEFAULT_CONTEXT_RADIUS,
): ContextSummary {
  const prefixStart = snapBackward(text, Math.max(0, range.start - radius));
  const suffixEnd = snapForward(text, Math.min(text.length, range.end + radius));
  return {
    prefix: text.slice(prefixStart, range.start),
    suffix: text.slice(range.end, suffixEnd),
    target: text.slice(range.start, range.end),
  };
}
