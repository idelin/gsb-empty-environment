// Independent full interpreter: recomputes every property of every node from
// scratch, with no caching, no dependency graph and no incremental shortcuts.
// It shares only the parser and data types with the incremental engine; the
// matcher, cascade ordering and variable resolution are re-implemented here so
// the differential tests cross-check the real computation.

import { parseStylesheet } from '../css/parser.ts';
import type { Rule } from '../css/parser.ts';
import type { Compound, Selector, SelectorPart } from '../css/selector.ts';
import { parseValueTokens } from '../css/values.ts';
import type { ValueToken } from '../css/values.ts';
import type { DocNode } from './model.ts';
import { buildChain, isCustomProperty, makeKey, REGULAR_PROPERTIES } from './computed.ts';
import type { ComputedValue, Declaration, VarResolution } from './computed.ts';

function compoundMatches(node: DocNode, compound: Compound): boolean {
  for (const simple of compound.simples) {
    switch (simple.kind) {
      case 'universal':
        break;
      case 'type':
        if (node.tag !== simple.tag) return false;
        break;
      case 'class':
        if (!node.classes.has(simple.name)) return false;
        break;
      case 'id':
        if (node.idAttr !== simple.id) return false;
        break;
      case 'state':
        if (!node.states.has(simple.name)) return false;
        break;
      case 'first-child':
        if (node.parent !== null && node.parent.children[0] !== node) return false;
        break;
      case 'last-child':
        if (node.parent !== null && node.parent.children[node.parent.children.length - 1] !== node) return false;
        break;
    }
  }
  return true;
}

function partMatches(node: DocNode, parts: SelectorPart[], i: number): boolean {
  if (!compoundMatches(node, parts[i].compound)) return false;
  if (i === 0) return true;
  if (parts[i].combinator === '>') {
    return node.parent !== null && partMatches(node.parent, parts, i - 1);
  }
  let ancestor = node.parent;
  while (ancestor !== null) {
    if (partMatches(ancestor, parts, i - 1)) return true;
    ancestor = ancestor.parent;
  }
  return false;
}

function selectorMatches(node: DocNode, selector: Selector): boolean {
  return partMatches(node, selector.parts, selector.parts.length - 1);
}

function cmpDecl(a: Declaration, b: Declaration): number {
  if (a.inline !== b.inline) return a.inline ? 1 : -1;
  for (let k = 0; k < 3; k++) {
    if (a.specificity[k] !== b.specificity[k]) return a.specificity[k] - b.specificity[k];
  }
  return a.ruleId - b.ruleId;
}

type VarResult = { ok: true; value: string } | { ok: false; reason: 'undefined' | 'cycle' | 'invalid'; cyclePath?: string[] };

export function computeAllStyles(
  nodes: DocNode[],
  css: string,
  inline: Map<number, Map<string, string>>,
): Map<string, ComputedValue> {
  const rules: Rule[] = parseStylesheet(css);
  const nodeById = new Map(nodes.map((n) => [n.id, n]));

  // Candidate declarations per node.
  const declsByNode = new Map<number, Declaration[]>();
  for (const node of nodes) {
    const decls: Declaration[] = [];
    for (const rule of rules) {
      for (const entry of rule.selectors) {
        if (selectorMatches(node, entry.selector)) {
          for (const d of rule.declarations) {
            decls.push({
              ruleId: rule.id,
              inline: false,
              selectorText: entry.text,
              specificity: entry.specificity,
              prop: d.prop,
              raw: d.raw,
              tokens: d.tokens,
            });
          }
        }
      }
    }
    const inlineMap = inline.get(node.id);
    if (inlineMap) {
      for (const [prop, raw] of inlineMap) {
        decls.push({ ruleId: -1, inline: true, selectorText: 'inline', specificity: [0, 0, 0], prop, raw, tokens: parseValueTokens(raw) });
      }
    }
    declsByNode.set(node.id, decls);
  }

  const knownCustom = new Set<string>();
  for (const rule of rules) {
    for (const d of rule.declarations) if (isCustomProperty(d.prop)) knownCustom.add(d.prop);
  }
  for (const map of inline.values()) {
    for (const prop of map.keys()) if (isCustomProperty(prop)) knownCustom.add(prop);
  }

  const memo = new Map<string, ComputedValue>();
  const stack: string[] = [];
  const cyclic = new Set<string>();
  const cyclicPaths = new Map<string, string[]>();

  const resolveVar = (nodeId: number, name: string): VarResult => {
    const key = makeKey(nodeId, name);
    const value = compute(key);
    if (value.value === null) {
      return { ok: false, reason: value.invalidReason ?? 'undefined', cyclePath: value.cyclePath };
    }
    return { ok: true, value: value.value };
  };

  const substitute = (tokens: ValueToken[], nodeId: number, vars: VarResolution[]): { ok: true; text: string } | { ok: false; reason: 'undefined' | 'cycle' | 'invalid'; cyclePath?: string[] } => {
    let text = '';
    for (const token of tokens) {
      if (token.kind === 'text') {
        text += token.text;
        continue;
      }
      const entry: VarResolution = { name: token.name, status: 'invalid' };
      vars.push(entry);
      const resolved = resolveVar(nodeId, token.name);
      if (resolved.ok) {
        entry.status = 'resolved';
        entry.value = resolved.value;
        text += resolved.value;
        continue;
      }
      entry.reason = resolved.reason;
      if (resolved.cyclePath) entry.cyclePath = resolved.cyclePath;
      if (token.fallback) {
        const sub = substitute(token.fallback, nodeId, vars);
        if (sub.ok) {
          entry.status = 'fallback';
          entry.value = sub.text.trim();
          text += sub.text;
          continue;
        }
        return { ok: false, reason: sub.reason, cyclePath: sub.cyclePath };
      }
      return { ok: false, reason: resolved.reason, cyclePath: resolved.cyclePath };
    }
    return { ok: true, text };
  };

  const compute = (key: string): ComputedValue => {
    const cached = memo.get(key);
    if (cached) return cached;
    const [nodeId, prop] = [Number(key.slice(0, key.indexOf('|'))), key.slice(key.indexOf('|') + 1)];
    const node = nodeById.get(nodeId)!;
    const hit = stack.indexOf(key);
    if (hit >= 0) {
      const cyclePath = stack.slice(hit).map((k) => k.slice(k.indexOf('|') + 1)).sort();
      for (let j = hit; j < stack.length; j++) {
        cyclic.add(stack[j]);
        cyclicPaths.set(stack[j], cyclePath);
      }
      return { prop, value: null, source: { kind: 'invalid', unsetTo: 'initial' }, chain: [], vars: [], invalidReason: 'cycle', cyclePath };
    }
    const allDecls = (declsByNode.get(nodeId) ?? []).filter((d) => d.prop === prop).sort(cmpDecl);
    const parentId = node.parent ? node.parent.id : null;
    let result: ComputedValue;
    stack.push(key);
    if (isCustomProperty(prop)) {
      result = computeCustom(nodeId, prop, parentId, allDecls);
    } else {
      result = computeRegular(nodeId, prop, parentId, allDecls);
    }
    stack.pop();
    if (cyclic.has(key)) {
      result = { ...result, value: null, source: { kind: 'invalid', unsetTo: 'initial' }, invalidReason: 'cycle', cyclePath: cyclicPaths.get(key) };
    }
    memo.set(key, result);
    return result;
  };

  const computeCustom = (nodeId: number, prop: string, parentId: number | null, decls: Declaration[]): ComputedValue => {
    if (decls.length === 0) {
      if (parentId !== null) {
        const inherited = compute(makeKey(parentId, prop));
        return { prop, value: inherited.value, source: { kind: 'inherited', from: parentId }, chain: [], vars: [], invalidReason: inherited.invalidReason, cyclePath: inherited.cyclePath };
      }
      return { prop, value: null, source: { kind: 'initial' }, chain: [], vars: [], invalidReason: 'undefined' };
    }
    const winner = decls[decls.length - 1];
    const vars: VarResolution[] = [];
    const res = substitute(winner.tokens, nodeId, vars);
    if (res.ok) {
      return { prop, value: res.text.trim(), source: { kind: 'declaration', ruleId: winner.ruleId, inline: winner.inline, selectorText: winner.selectorText, raw: winner.raw }, chain: buildChain(decls, false), vars };
    }
    return { prop, value: null, source: { kind: 'invalid', unsetTo: 'initial' }, chain: buildChain(decls, true), vars, invalidReason: res.reason, cyclePath: res.cyclePath };
  };

  const computeRegular = (nodeId: number, prop: string, parentId: number | null, decls: Declaration[]): ComputedValue => {
    const meta = REGULAR_PROPERTIES[prop];
    if (decls.length > 0) {
      const winner = decls[decls.length - 1];
      const vars: VarResolution[] = [];
      const res = substitute(winner.tokens, nodeId, vars);
      if (res.ok) {
        return { prop, value: res.text.trim(), source: { kind: 'declaration', ruleId: winner.ruleId, inline: winner.inline, selectorText: winner.selectorText, raw: winner.raw }, chain: buildChain(decls, false), vars };
      }
      const chain = buildChain(decls, true);
      if (meta.inherited && parentId !== null) {
        const inherited = compute(makeKey(parentId, prop));
        return { prop, value: inherited.value, source: { kind: 'invalid', unsetTo: 'inherited', from: parentId }, chain, vars };
      }
      return { prop, value: meta.initial, source: { kind: 'invalid', unsetTo: 'initial' }, chain, vars };
    }
    if (meta.inherited && parentId !== null) {
      const inherited = compute(makeKey(parentId, prop));
      return { prop, value: inherited.value, source: { kind: 'inherited', from: parentId }, chain: [], vars: [] };
    }
    return { prop, value: meta.initial, source: { kind: 'initial' }, chain: [], vars: [] };
  };

  const out = new Map<string, ComputedValue>();
  for (const node of nodes) {
    for (const prop of Object.keys(REGULAR_PROPERTIES)) out.set(makeKey(node.id, prop), compute(makeKey(node.id, prop)));
    for (const prop of knownCustom) out.set(makeKey(node.id, prop), compute(makeKey(node.id, prop)));
  }
  return out;
}
