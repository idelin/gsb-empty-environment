import type { Span } from "./ast.ts";
import { TemplateSyntaxError } from "./errors.ts";
import type { Source } from "./source.ts";

export type Token =
  | { type: "text"; span: Span; text: string }
  | { type: "expr"; span: Span; content: string; contentOffset: number }
  | { type: "tag"; span: Span; content: string; contentOffset: number };

/**
 * Splits a template into text, `{{ expr }}` and `{% tag %}` tokens.
 * `contentOffset` is the absolute offset where `content` begins, so the
 * parser can compute precise spans for what is inside the delimiters.
 */
export function lex(source: Source): Token[] {
  const { text } = source;
  const tokens: Token[] = [];
  let cursor = 0;

  while (cursor < text.length) {
    const nextVar = text.indexOf("{{", cursor);
    const nextTag = text.indexOf("{%", cursor);
    let open = -1;
    let isVar = false;
    if (nextVar === -1) open = nextTag;
    else if (nextTag === -1) {
      open = nextVar;
      isVar = true;
    } else if (nextVar < nextTag) {
      open = nextVar;
      isVar = true;
    } else open = nextTag;

    if (open === -1) {
      tokens.push({ type: "text", span: { start: cursor, end: text.length }, text: text.slice(cursor) });
      break;
    }
    if (open > cursor) {
      tokens.push({ type: "text", span: { start: cursor, end: open }, text: text.slice(cursor, open) });
    }

    const close = isVar ? "}}" : "%}";
    const contentOffset = open + 2;
    const closeAt = text.indexOf(close, contentOffset);
    if (closeAt === -1) {
      throw new TemplateSyntaxError(`Unclosed '${isVar ? "{{" : "{%"}'`, { start: open, end: open + 2 }, source);
    }
    tokens.push({
      type: isVar ? "expr" : "tag",
      span: { start: open, end: closeAt + 2 },
      content: text.slice(contentOffset, closeAt),
      contentOffset,
    });
    cursor = closeAt + 2;
  }
  return tokens;
}
