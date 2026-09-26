// Stylesheet parser: rules of the form `selectorA, selectorB { prop: value; --var: value; }`.
// No at-rules, no nested blocks; values may contain var() with nested fallbacks.

import { parseSelector, specificityOf } from './selector.ts';
import type { Selector, Specificity } from './selector.ts';
import { parseValueTokens } from './values.ts';
import type { ValueToken } from './values.ts';
import { isCustomProperty, REGULAR_PROPERTIES } from '../engine/computed.ts';

export interface RawDeclaration {
  prop: string;
  raw: string;
  tokens: ValueToken[];
}

export interface SelectorEntry {
  text: string;
  selector: Selector;
  specificity: Specificity;
}

export interface Rule {
  id: number;
  selectorText: string;
  selectors: SelectorEntry[];
  declarations: RawDeclaration[];
}

export function parseStylesheet(css: string): Rule[] {
  const src = css.replace(/\/\*[\s\S]*?\*\//g, '');
  const rules: Rule[] = [];
  let i = 0;
  while (i < src.length) {
    while (i < src.length && /\s/.test(src[i])) i++;
    if (i >= src.length) break;
    const preludeStart = i;
    while (i < src.length && src[i] !== '{') i++;
    if (i >= src.length) throw new Error('parse error: expected "{"');
    const selectorText = src.slice(preludeStart, i).trim();
    i++; // consume '{'
    const bodyStart = i;
    let depth = 0;
    while (i < src.length && (src[i] !== '}' || depth > 0)) {
      if (src[i] === '(') depth++;
      else if (src[i] === ')') depth--;
      else if (src[i] === '{') throw new Error('parse error: nested blocks are not supported');
      i++;
    }
    if (i >= src.length) throw new Error('parse error: expected "}"');
    const body = src.slice(bodyStart, i);
    i++; // consume '}'
    const selectors = selectorText
      .split(',')
      .map((part) => part.trim())
      .filter((part) => part.length > 0)
      .map((part) => {
        const selector = parseSelector(part);
        return { text: selector.text, selector, specificity: specificityOf(selector) };
      });
    if (selectors.length === 0) throw new Error(`parse error: empty selector before "${body.slice(0, 20)}"`);
    rules.push({
      id: rules.length,
      selectorText,
      selectors,
      declarations: parseDeclarations(body),
    });
  }
  return rules;
}

function parseDeclarations(body: string): RawDeclaration[] {
  const out: RawDeclaration[] = [];
  for (const chunk of splitTopLevel(body, ';')) {
    const colon = chunk.indexOf(':');
    if (colon < 0) continue;
    const rawProp = chunk.slice(0, colon).trim();
    if (!rawProp) continue;
    const prop = isCustomProperty(rawProp) ? rawProp : rawProp.toLowerCase();
    if (!isCustomProperty(prop) && !(prop in REGULAR_PROPERTIES)) continue; // unknown property: dropped
    const raw = chunk.slice(colon + 1).trim();
    if (!raw) continue;
    out.push({ prop, raw, tokens: parseValueTokens(raw) });
  }
  return out;
}

function splitTopLevel(s: string, sep: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '(') depth++;
    else if (s[i] === ')') depth--;
    else if (s[i] === sep && depth === 0) {
      parts.push(s.slice(start, i));
      start = i + 1;
    }
  }
  parts.push(s.slice(start));
  return parts;
}
