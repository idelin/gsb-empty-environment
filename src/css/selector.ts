// Simplified selector model: type / .class / #id / * / :state / :first-child / :last-child,
// compound selectors, descendant and child combinators, comma lists (split by the parser).

import type { DocNode } from '../engine/model.ts';

export type Specificity = [number, number, number]; // [ids, classes+states, types]

export type SimpleSelector =
  | { kind: 'universal' }
  | { kind: 'type'; tag: string }
  | { kind: 'class'; name: string }
  | { kind: 'id'; id: string }
  | { kind: 'state'; name: string }
  | { kind: 'first-child' }
  | { kind: 'last-child' };

export interface Compound {
  simples: SimpleSelector[];
}

export interface SelectorPart {
  combinator: ' ' | '>' | null; // combinator binding this part to the previous one
  compound: Compound;
}

export interface Selector {
  text: string;
  parts: SelectorPart[];
}

const STATE_PSEUDOS = new Set(['hover', 'focus', 'active', 'checked', 'disabled']);

function isIdentChar(ch: string): boolean {
  return /[a-zA-Z0-9_-]/.test(ch);
}

export function parseSelector(text: string): Selector {
  const s = text.trim();
  const parts: SelectorPart[] = [];
  let i = 0;
  let pending: ' ' | '>' | null = null;
  while (i < s.length) {
    let sawSpace = false;
    while (i < s.length && /\s/.test(s[i])) {
      i++;
      sawSpace = true;
    }
    if (i >= s.length) break;
    if (sawSpace && parts.length > 0 && pending === null) pending = ' ';
    if (s[i] === '>') {
      pending = '>';
      i++;
      continue;
    }
    const simples: SimpleSelector[] = [];
    while (i < s.length && !/[\s>]/.test(s[i])) {
      const ch = s[i];
      if (ch === '*') {
        simples.push({ kind: 'universal' });
        i++;
      } else if (ch === '.' || ch === '#') {
        const start = ++i;
        while (i < s.length && isIdentChar(s[i])) i++;
        const name = s.slice(start, i);
        if (!name) throw new Error(`bad selector "${text}"`);
        simples.push(ch === '.' ? { kind: 'class', name } : { kind: 'id', id: name });
      } else if (ch === ':') {
        const start = ++i;
        while (i < s.length && isIdentChar(s[i])) i++;
        const name = s.slice(start, i);
        if (name === 'first-child') simples.push({ kind: 'first-child' });
        else if (name === 'last-child') simples.push({ kind: 'last-child' });
        else if (STATE_PSEUDOS.has(name)) simples.push({ kind: 'state', name });
        else throw new Error(`unsupported pseudo-class ":${name}"`);
      } else {
        const start = i;
        while (i < s.length && isIdentChar(s[i])) i++;
        simples.push({ kind: 'type', tag: s.slice(start, i).toLowerCase() });
      }
    }
    if (simples.length === 0) throw new Error(`bad selector "${text}"`);
    parts.push({ combinator: parts.length === 0 ? null : (pending ?? ' '), compound: { simples } });
    pending = null;
  }
  if (parts.length === 0) throw new Error(`empty selector`);
  return { text: text.trim(), parts };
}

export function specificityOf(sel: Selector): Specificity {
  let ids = 0;
  let classes = 0;
  let types = 0;
  for (const part of sel.parts) {
    for (const simple of part.compound.simples) {
      if (simple.kind === 'id') ids++;
      else if (simple.kind === 'class' || simple.kind === 'state' || simple.kind === 'first-child' || simple.kind === 'last-child') classes++;
      else if (simple.kind === 'type') types++;
    }
  }
  return [ids, classes, types];
}

export function compareSpecificity(a: Specificity, b: Specificity): number {
  for (let k = 0; k < 3; k++) {
    if (a[k] !== b[k]) return a[k] - b[k];
  }
  return 0;
}

export function matchCompound(node: DocNode, compound: Compound): boolean {
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

// Right-to-left matching. Every node whose attributes were inspected is pushed
// to `consulted` (when provided); the incremental engine uses this to know
// exactly which attribute changes can flip this match.
export function matchSelector(node: DocNode, sel: Selector, consulted?: number[]): boolean {
  if (consulted) consulted.push(node.id);
  return matchFrom(node, sel.parts, sel.parts.length - 1, consulted);
}

function matchFrom(node: DocNode, parts: SelectorPart[], i: number, consulted?: number[]): boolean {
  if (!matchCompound(node, parts[i].compound)) return false;
  if (i === 0) return true;
  const combinator = parts[i].combinator;
  if (combinator === '>') {
    const parent = node.parent;
    if (parent === null) return false;
    if (consulted) consulted.push(parent.id);
    return matchFrom(parent, parts, i - 1, consulted);
  }
  let ancestor = node.parent;
  while (ancestor !== null) {
    if (consulted) consulted.push(ancestor.id);
    if (matchFrom(ancestor, parts, i - 1, consulted)) return true;
    ancestor = ancestor.parent;
  }
  return false;
}
