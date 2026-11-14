import { sanitizeMarkdown, parseMarkdown, renderMarkdownAst } from "./src/markdown.ts";
import type { MarkdownOptions } from "./src/markdown.ts";
import {
  ALLOWED_ATTRIBUTES,
  ALLOWED_TAGS,
  escapeAttribute,
  escapeHtmlText,
  sanitizeHtml,
  sanitizeHtmlToken,
} from "./src/html.ts";
import type { SanitizeHtmlOptions } from "./src/html.ts";
import {
  decodeHtmlEntitiesDeep,
  decodeHtmlEntitiesOnce,
  isSafeUrl,
  sanitizeUrl,
} from "./src/url.ts";
import type { SanitizeUrlOptions } from "./src/url.ts";

export {
  ALLOWED_ATTRIBUTES,
  ALLOWED_TAGS,
  decodeHtmlEntitiesDeep,
  decodeHtmlEntitiesOnce,
  escapeAttribute,
  escapeHtmlText,
  isSafeUrl,
  parseMarkdown,
  renderMarkdownAst,
  sanitizeHtml,
  sanitizeHtmlToken,
  sanitizeMarkdown,
  sanitizeUrl,
};

export type { MarkdownOptions, SanitizeHtmlOptions, SanitizeUrlOptions };
export type {
  MarkdownBlockNode,
  MarkdownDocument,
  MarkdownInlineNode,
  MarkdownListNode,
  MarkdownLinkNode,
  MarkdownImageNode,
  MarkdownHeadingNode,
  MarkdownParagraphNode,
  MarkdownCodeBlockNode,
  MarkdownTableNode,
} from "./src/markdown.ts";
export type {
  HtmlElementNode,
  HtmlNode,
  HtmlTextNode,
  HtmlToken,
} from "./src/html.ts";

/** Alias used by some documentation pipelines. */
export const sanitize = sanitizeMarkdown;
/** Render Markdown through the same sanitizing pipeline. */
export const renderMarkdown = sanitizeMarkdown;
/** Explicitly named alias for call sites that prefer the output format. */
export const sanitizeMarkdownToHtml = sanitizeMarkdown;

export default sanitizeMarkdown;

export function renderMarkdownSafe(
  markdown: string,
  options: MarkdownOptions = {},
): string {
  return sanitizeMarkdown(markdown, options);
}
