/**
 * Public API.
 *
 *   import { compile } from 'leftmost-longest-regex';
 *   const re = compile('(a*)(b*)');
 *   const m = re.exec('aab');
 *   // m.span        -> { start: 0, end: 3 }   (whole match)
 *   // m.groups[1]   -> { start: 0, end: 2 }   (group 1 span)
 *   // m.groups[2]   -> { start: 2, end: 2 }   (empty participating group)
 *
 * A group that did not participate in the match is reported as `undefined`,
 * which is distinct from a participating group that matched the empty string
 * (reported as a span with start === end).
 */

import { Node, parse } from './ast';
import { search, Span } from './matcher';

export { Span } from './matcher';
export { parse } from './ast';

export interface Match {
  /** Overall match span (leftmost-longest). */
  readonly span: Span;
  /** Matched text. */
  readonly text: string;
  /**
   * Capture spans. groups[0] is the whole match; groups[i] is the span of
   * capturing group i, or `undefined` if the group did not participate.
   * A group inside a star records the span of its last iteration.
   */
  readonly groups: ReadonlyArray<Span | undefined>;
}

export class Regex {
  readonly source: string;
  readonly groupCount: number;
  private readonly root: Node;

  constructor(pattern: string) {
    const parsed = parse(pattern);
    this.source = pattern;
    this.groupCount = parsed.groupCount;
    this.root = parsed.root;
  }

  /** Leftmost-longest search in `input`; null when nothing matches. */
  exec(input: string): Match | null {
    const m = search(this.root, input, this.groupCount);
    if (m === null) return null;
    const groups: Array<Span | undefined> = new Array<Span | undefined>(this.groupCount + 1);
    groups[0] = { start: m.start, end: m.end };
    for (let i = 1; i <= this.groupCount; i++) {
      groups[i] = m.captures[i];
    }
    const span = groups[0] as Span;
    return { span, text: input.slice(m.start, m.end), groups };
  }

  test(input: string): boolean {
    return this.exec(input) !== null;
  }
}

export function compile(pattern: string): Regex {
  return new Regex(pattern);
}
