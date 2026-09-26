// Selector matching + specificity.

import type { ComplexSelector, Compound, ElNode, Pseudo, Specificity } from './model.ts';

export function matches(el: ElNode, sel: ComplexSelector): boolean {
  return matchPart(el, sel.parts, sel.parts.length - 1);
}

function matchPart(el: ElNode, parts: ComplexSelector['parts'], i: number): boolean {
  if (!matchesCompound(el, parts[i].compound)) return false;
  if (i === 0) return true;
  const combinator = parts[i].combinator;
  if (combinator === '>') {
    return el.parent !== null && matchPart(el.parent, parts, i - 1);
  }
  // descendant combinator
  let anc = el.parent;
  while (anc !== null) {
    if (matchPart(anc, parts, i - 1)) return true;
    anc = anc.parent;
  }
  return false;
}

export function matchesCompound(el: ElNode, c: Compound): boolean {
  if (c.tag !== undefined && el.tag !== c.tag) return false;
  if (c.id !== undefined && el.name !== c.id) return false;
  for (const cls of c.classes) {
    if (!el.classes.includes(cls)) return false;
  }
  for (const pseudo of c.pseudos) {
    if (!matchesPseudo(el, pseudo)) return false;
  }
  return true;
}

function matchesPseudo(el: ElNode, p: Pseudo): boolean {
  switch (p.name) {
    case 'first-child':
      return el.parent === null || el.parent.children[0] === el;
    case 'last-child':
      return el.parent === null || el.parent.children[el.parent.children.length - 1] === el;
    case 'nth-child': {
      const index = el.parent === null ? 1 : el.parent.children.indexOf(el) + 1;
      return matchesNth(index, p.arg ?? '');
    }
    case 'not':
      return p.not !== undefined && !matchesCompound(el, p.not);
    default:
      // state flags such as :hover, :focus
      return el.states.has(p.name);
  }
}

function matchesNth(index: number, arg: string): boolean {
  const a = arg.trim();
  if (a === 'odd') return index % 2 === 1;
  if (a === 'even') return index % 2 === 0;
  const n = Number(a);
  return Number.isInteger(n) && index === n;
}

export function specificityOf(sel: ComplexSelector): Specificity {
  let ids = 0;
  let cls = 0;
  let tags = 0;
  for (const part of sel.parts) {
    const [i, c, t] = compoundSpecificity(part.compound);
    ids += i;
    cls += c;
    tags += t;
  }
  return [ids, cls, tags];
}

function compoundSpecificity(c: Compound): Specificity {
  let ids = c.id !== undefined ? 1 : 0;
  let cls = c.classes.length;
  let tags = c.tag !== undefined ? 1 : 0;
  for (const p of c.pseudos) {
    if (p.name === 'not' && p.not !== undefined) {
      const [i, cc, t] = compoundSpecificity(p.not);
      ids += i;
      cls += cc;
      tags += t;
    } else {
      cls++;
    }
  }
  return [ids, cls, tags];
}

export function compareSpecificity(a: Specificity, b: Specificity): number {
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] - b[i];
  }
  return 0;
}
