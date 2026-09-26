// Stylesheet parser for the simplified CSS subset.

import type { ComplexSelector, Declaration, Pseudo, Rule, SelectorPart } from './model.ts';
import type { Compound } from './model.ts';

export function parseStylesheet(text: string): Rule[] {
  const cleaned = text.replace(/\/\*[\s\S]*?\*\//g, '');
  const rules: Rule[] = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m: RegExpExecArray | null;
  let order = 0;
  while ((m = re.exec(cleaned)) !== null) {
    const selectorText = m[1].trim();
    const declarations = parseDeclarations(m[2]);
    for (const single of splitTopLevelCommas(selectorText)) {
      const source = single.trim();
      if (!source) continue;
      rules.push({ selector: parseSelector(source), declarations, order: order++, source });
    }
  }
  return rules;
}

function parseDeclarations(body: string): Declaration[] {
  const out: Declaration[] = [];
  for (const chunk of body.split(';')) {
    const idx = chunk.indexOf(':');
    if (idx === -1) continue;
    const prop = chunk.slice(0, idx).trim();
    const value = chunk.slice(idx + 1).trim();
    if (prop && value) out.push({ prop, value });
  }
  return out;
}

function splitTopLevelCommas(text: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let cur = '';
  for (const c of text) {
    if (c === '(') depth++;
    else if (c === ')') depth--;
    if (c === ',' && depth === 0) {
      parts.push(cur);
      cur = '';
    } else {
      cur += c;
    }
  }
  parts.push(cur);
  return parts;
}

export function parseSelector(text: string): ComplexSelector {
  const tokens = text.match(/[>]|[^\s>]+/g) ?? [];
  const parts: SelectorPart[] = [];
  let pending: ' ' | '>' | null = null;
  for (const tok of tokens) {
    if (tok === '>') {
      pending = '>';
      continue;
    }
    parts.push({
      compound: parseCompound(tok),
      combinator: parts.length === 0 ? null : pending ?? ' ',
    });
    pending = null;
  }
  if (parts.length === 0) throw new Error(`empty selector: ${text}`);
  return { parts };
}

export function parseCompound(str: string): Compound {
  const out: Compound = { classes: [], pseudos: [] };
  let i = 0;
  const tagMatch = /^[a-zA-Z*][\w-]*/.exec(str);
  if (tagMatch) {
    if (tagMatch[0] !== '*') out.tag = tagMatch[0];
    i = tagMatch[0].length;
  }
  while (i < str.length) {
    const ch = str[i];
    if (ch === '#') {
      const m = /^[\w-]+/.exec(str.slice(i + 1));
      if (!m) throw new Error(`bad id in selector: ${str}`);
      out.id = m[0];
      i += 1 + m[0].length;
    } else if (ch === '.') {
      const m = /^[\w-]+/.exec(str.slice(i + 1));
      if (!m) throw new Error(`bad class in selector: ${str}`);
      out.classes.push(m[0]);
      i += 1 + m[0].length;
    } else if (ch === ':') {
      const m = /^[\w-]+/.exec(str.slice(i + 1));
      if (!m) throw new Error(`bad pseudo in selector: ${str}`);
      const name = m[0];
      i += 1 + m[0].length;
      let arg: string | undefined;
      if (str[i] === '(') {
        const close = str.indexOf(')', i);
        if (close === -1) throw new Error(`unbalanced pseudo arg: ${str}`);
        arg = str.slice(i + 1, close);
        i = close + 1;
      }
      const pseudo: Pseudo = { name, arg };
      if (name === 'not' && arg !== undefined) pseudo.not = parseCompound(arg);
      out.pseudos.push(pseudo);
    } else {
      throw new Error(`unexpected '${ch}' in selector: ${str}`);
    }
  }
  return out;
}
