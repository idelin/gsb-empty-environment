import type {
  MatcherObject,
  NodeMatcher,
  NormalizedNode,
} from './types.ts';

interface AttributePredicate {
  name: string;
  operator: 'exists' | '=' | '~=' | '*=' | '^=' | '$=' | '|=';
  value: string;
  insensitive: boolean;
}

interface CompoundSelector {
  tag: string | null;
  ids: string[];
  classes: string[];
  attributes: AttributePredicate[];
  not: CompoundSelector[];
  scope: boolean;
}

interface ComplexPart {
  combinator: ' ' | '>' | '+' | '~' | null;
  compound: CompoundSelector;
}

export class SelectorSyntaxError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SelectorSyntaxError';
  }
}

function isElement(node: NormalizedNode): boolean {
  return node.kind === 'element';
}

function attribute(node: NormalizedNode, name: string): string | undefined {
  return node.attributeMap[name.toLowerCase()]?.value;
}

function hasAttribute(node: NormalizedNode, name: string): boolean {
  return Boolean(node.attributeMap[name.toLowerCase()]);
}

function splitTopLevel(value: string, delimiter = ','): string[] {
  const parts: string[] = [];
  let current = '';
  let bracketDepth = 0;
  let parenDepth = 0;
  let quote: string | null = null;

  for (let index = 0; index < value.length; index += 1) {
    const char = value[index];
    if (quote) {
      current += char;
      if (char === '\\' && index + 1 < value.length) {
        index += 1;
        current += value[index];
      } else if (char === quote) {
        quote = null;
      }
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      current += char;
      continue;
    }
    if (char === '[') bracketDepth += 1;
    if (char === ']') bracketDepth -= 1;
    if (char === '(') parenDepth += 1;
    if (char === ')') parenDepth -= 1;
    if (char === delimiter && bracketDepth === 0 && parenDepth === 0) {
      parts.push(current);
      current = '';
    } else {
      current += char;
    }
  }
  parts.push(current);
  return parts;
}

function splitComplex(selector: string): Array<{ text: string; combinator: ' ' | '>' | '+' | '~' | null }> {
  const result: Array<{ text: string; combinator: ' ' | '>' | '+' | '~' | null }> = [];
  let current = '';
  let bracketDepth = 0;
  let parenDepth = 0;
  let quote: string | null = null;
  let pendingCombinator: ' ' | '>' | '+' | '~' | null = null;

  const flush = () => {
    const text = current.trim();
    if (text) {
      result.push({ text, combinator: pendingCombinator });
      pendingCombinator = null;
    }
    current = '';
  };

  for (let index = 0; index < selector.length; index += 1) {
    const char = selector[index];
    if (quote) {
      current += char;
      if (char === '\\' && index + 1 < selector.length) {
        index += 1;
        current += selector[index];
      } else if (char === quote) {
        quote = null;
      }
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      current += char;
      continue;
    }
    if (char === '[') bracketDepth += 1;
    if (char === ']') bracketDepth -= 1;
    if (char === '(') parenDepth += 1;
    if (char === ')') parenDepth -= 1;

    if (bracketDepth === 0 && parenDepth === 0) {
      if (char === '>' || char === '+' || char === '~') {
        flush();
        pendingCombinator = char;
        continue;
      }
      if (/\s/.test(char)) {
        if (current.trim()) {
          flush();
          pendingCombinator = ' ';
        }
        continue;
      }
    }
    current += char;
  }
  flush();
  return result;
}

function readEscaped(text: string, index: number): { value: string; next: number } {
  if (text[index] === '\\' && index + 1 < text.length) {
    return { value: text[index + 1], next: index + 2 };
  }
  return { value: text[index], next: index + 1 };
}

function parseCompound(text: string): CompoundSelector {
  const compound: CompoundSelector = {
    tag: null,
    ids: [],
    classes: [],
    attributes: [],
    not: [],
    scope: false,
  };
  let index = 0;

  if (text[index] === '*') {
    compound.tag = '*';
    index += 1;
  } else if (index < text.length && /[A-Za-z_]/.test(text[index])) {
    let tag = '';
    while (index < text.length && /[A-Za-z0-9_-]/.test(text[index])) {
      tag += text[index];
      index += 1;
    }
    compound.tag = tag.toLowerCase();
  }

  while (index < text.length) {
    const char = text[index];
    if (char === '.') {
      index += 1;
      let name = '';
      while (index < text.length && !/[.#:[\]()>+~,\s]/.test(text[index])) {
        const escaped = readEscaped(text, index);
        name += escaped.value;
        index = escaped.next;
      }
      if (!name) throw new SelectorSyntaxError(`Empty class in selector "${text}"`);
      compound.classes.push(name);
      continue;
    }
    if (char === '#') {
      index += 1;
      let name = '';
      while (index < text.length && !/[.#:[\]()>+~,\s]/.test(text[index])) {
        const escaped = readEscaped(text, index);
        name += escaped.value;
        index = escaped.next;
      }
      if (!name) throw new SelectorSyntaxError(`Empty id in selector "${text}"`);
      compound.ids.push(name);
      continue;
    }
    if (char === '[') {
      const end = findClosing(text, index, '[', ']');
      if (end < 0) throw new SelectorSyntaxError(`Unclosed attribute selector in "${text}"`);
      compound.attributes.push(parseAttribute(text.slice(index + 1, end)));
      index = end + 1;
      continue;
    }
    if (char === ':') {
      const pseudo = text.slice(index).match(/^:([A-Za-z-]+)/);
      if (!pseudo) throw new SelectorSyntaxError(`Invalid pseudo selector in "${text}"`);
      const name = pseudo[1].toLowerCase();
      index += pseudo[0].length;
      if (name === 'scope') {
        compound.scope = true;
        continue;
      }
      if (name === 'not' && text[index] === '(') {
        const end = findClosing(text, index, '(', ')');
        if (end < 0) throw new SelectorSyntaxError(`Unclosed :not() in "${text}"`);
        const inner = text.slice(index + 1, end);
        for (const part of splitTopLevel(inner)) {
          compound.not.push(parseCompound(part.trim()));
        }
        index = end + 1;
        continue;
      }
      throw new SelectorSyntaxError(`Unsupported pseudo selector ":${name}"`);
    }
    throw new SelectorSyntaxError(`Unexpected character "${char}" in selector "${text}"`);
  }

  if (!compound.tag && !compound.ids.length && !compound.classes.length && !compound.attributes.length && !compound.not.length && !compound.scope) {
    throw new SelectorSyntaxError(`Empty selector "${text}"`);
  }
  return compound;
}

function findClosing(text: string, start: number, open: string, close: string): number {
  let depth = 0;
  let quote: string | null = null;
  for (let index = start; index < text.length; index += 1) {
    const char = text[index];
    if (quote) {
      if (char === '\\') index += 1;
      else if (char === quote) quote = null;
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === open) depth += 1;
    if (char === close) {
      depth -= 1;
      if (depth === 0) return index;
    }
  }
  return -1;
}

function parseAttribute(body: string): AttributePredicate {
  const match = body
    .trim()
    .match(/^([A-Za-z0-9_:-]+)\s*(?:(~=|\*=|\^=|\$=|\|=|=)\s*(?:"([^"]*)"|'([^']*)'|([^\s]+)))?\s*([iIsS])?$/);
  if (!match) {
    throw new SelectorSyntaxError(`Invalid attribute selector "[${body}]"`);
  }
  const operator = (match[2] as AttributePredicate['operator'] | undefined) ?? 'exists';
  const value = match[3] ?? match[4] ?? match[5] ?? '';
  return {
    name: match[1].toLowerCase(),
    operator,
    value,
    insensitive: (match[6] ?? '').toLowerCase() === 'i',
  };
}

function splitSelectorChains(selector: string): ComplexPart[][] {
  const chains: ComplexPart[][] = [];
  for (const complex of splitTopLevel(selector)) {
    const trimmed = complex.trim();
    if (!trimmed) continue;
    const parts = splitComplex(trimmed);
    if (!parts.length) throw new SelectorSyntaxError(`Empty selector in "${selector}"`);
    chains.push(
      parts.map((part, index) => ({
        combinator: index === 0 ? null : part.combinator,
        compound: parseCompound(part.text),
      })),
    );
  }
  return chains;
}

function matchAttribute(node: NormalizedNode, predicate: AttributePredicate): boolean {
  if (predicate.operator === 'exists') return hasAttribute(node, predicate.name);
  const value = attribute(node, predicate.name);
  if (value === undefined) return false;
  const expected = predicate.insensitive ? predicate.value.toLowerCase() : predicate.value;
  const actual = predicate.insensitive ? value.toLowerCase() : value;
  switch (predicate.operator) {
    case '=':
      return actual === expected;
    case '~=':
      return actual.split(/\s+/).includes(expected);
    case '*=':
      return actual.includes(expected);
    case '^=':
      return actual.startsWith(expected);
    case '$=':
      return actual.endsWith(expected);
    case '|=':
      return actual === expected || actual.startsWith(`${expected}-`);
    default:
      return false;
  }
}

function matchCompound(node: NormalizedNode, compound: CompoundSelector): boolean {
  if (!isElement(node)) return false;
  if (compound.tag && compound.tag !== '*' && compound.tag !== node.tagName) {
    // Selectors use the actual tag name, not the synthetic #document name.
    return false;
  }
  if (compound.scope && node.kind !== 'element') return false;
  for (const id of compound.ids) {
    if (attribute(node, 'id') !== id) return false;
  }
  if (compound.classes.length) {
    const classes = (attribute(node, 'class') ?? '').split(/\s+/).filter(Boolean);
    for (const name of compound.classes) {
      if (!classes.includes(name)) return false;
    }
  }
  for (const predicate of compound.attributes) {
    if (!matchAttribute(node, predicate)) return false;
  }
  for (const excluded of compound.not) {
    if (matchCompound(node, excluded)) return false;
  }
  return true;
}

function parentElement(node: NormalizedNode): NormalizedNode | null {
  let parent = node.parent;
  while (parent && parent.kind !== 'element') parent = parent.parent;
  return parent;
}

function previousElementSibling(node: NormalizedNode): NormalizedNode | null {
  if (!node.parent) return null;
  const siblings = node.parent.children;
  let found = false;
  for (let index = siblings.length - 1; index >= 0; index -= 1) {
    const sibling = siblings[index];
    if (sibling === node) {
      found = true;
      continue;
    }
    if (found && sibling.kind === 'element') return sibling;
  }
  return null;
}

function previousElementSiblings(node: NormalizedNode): NormalizedNode[] {
  if (!node.parent) return [];
  const result: NormalizedNode[] = [];
  for (const sibling of node.parent.children) {
    if (sibling === node) break;
    if (sibling.kind === 'element') result.push(sibling);
  }
  return result;
}

function matchChain(node: NormalizedNode, chain: ComplexPart[]): boolean {
  if (!chain.length) return false;
  if (!matchCompound(node, chain[chain.length - 1].compound)) return false;
  let current: NormalizedNode = node;
  for (let index = chain.length - 2; index >= 0; index -= 1) {
    const combinator = chain[index + 1].combinator ?? ' ';
    const target = chain[index].compound;
    if (combinator === '>') {
      const parent = parentElement(current);
      if (!parent || !matchCompound(parent, target)) return false;
      current = parent;
    } else if (combinator === '+') {
      const previous = previousElementSibling(current);
      if (!previous || !matchCompound(previous, target)) return false;
      current = previous;
    } else if (combinator === '~') {
      const previous = previousElementSiblings(current).reverse().find((candidate) => matchCompound(candidate, target));
      if (!previous) return false;
      current = previous;
    } else {
      let ancestor = parentElement(current);
      while (ancestor && !matchCompound(ancestor, target)) ancestor = parentElement(ancestor);
      if (!ancestor) return false;
      current = ancestor;
    }
  }
  return true;
}

export function matchesSelector(node: NormalizedNode, selector: string): boolean {
  const chains = splitSelectorChains(selector);
  return chains.some((chain) => matchChain(node, chain));
}

function matchObject(node: NormalizedNode, matcher: MatcherObject): boolean {
  if (matcher.all && !matcher.all.every((child) => createNodeMatcher(child)(node))) return false;
  if (matcher.any && !matcher.any.some((child) => createNodeMatcher(child)(node))) return false;
  if (matcher.not && createNodeMatcher(matcher.not)(node)) return false;
  if (matcher.predicate && !matcher.predicate(node)) return false;

  if (matcher.tagName !== undefined || matcher.tag !== undefined) {
    const values = toArray(matcher.tagName ?? matcher.tag);
    if (!values.map((value) => value.toLowerCase()).includes(node.tagName)) return false;
  }
  if (matcher.id !== undefined) {
    if (!toArray(matcher.id).includes(attribute(node, 'id') ?? '')) return false;
  }
  if (matcher.class !== undefined || matcher.className !== undefined) {
    const expected = toArray(matcher.class ?? matcher.className);
    const actual = (attribute(node, 'class') ?? '').split(/\s+/).filter(Boolean);
    if (!expected.every((name) => actual.includes(name))) return false;
  }
  if (matcher.attribute !== undefined) {
    if (!matchAttributeConfig(node, matcher.attribute)) return false;
  }
  if (matcher.attributes !== undefined) {
    if (!matchAttributeConfig(node, matcher.attributes)) return false;
  }
  if (matcher.selector !== undefined) {
    const selectors = toArray(matcher.selector);
    if (!selectors.some((selector) => matchesSelector(node, selector))) return false;
  }
  return true;
}

function matchAttributeConfig(
  node: NormalizedNode,
  config: string | string[] | Record<string, string | number | boolean>,
): boolean {
  if (typeof config === 'string') return hasAttribute(node, config);
  if (Array.isArray(config)) return config.every((name) => hasAttribute(node, name));
  for (const [name, expected] of Object.entries(config)) {
    if (expected === false) {
      if (hasAttribute(node, name)) return false;
      continue;
    }
    if (expected === true) {
      if (!hasAttribute(node, name)) return false;
      continue;
    }
    if (attribute(node, name) !== String(expected)) return false;
  }
  return true;
}

function toArray<T>(value: T | T[] | undefined): T[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? value : [value];
}

export function createNodeMatcher(matcher: NodeMatcher): (node: NormalizedNode) => boolean {
  if (typeof matcher === 'function') return matcher;
  if (typeof matcher === 'string') return (node) => matchesSelector(node, matcher);
  if (Array.isArray(matcher)) {
    const children = matcher.map((child) => createNodeMatcher(child));
    return (node) => children.some((child) => child(node));
  }
  return (node) => matchObject(node, matcher);
}

/** Cache the compiled matcher rather than its source object. */
const matcherCache = new WeakMap<object, (node: NormalizedNode) => boolean>();

export function compileMatcher(matcher: NodeMatcher | undefined): ((node: NormalizedNode) => boolean) | null {
  if (matcher === undefined || matcher === null) return null;
  if (typeof matcher === 'function') return matcher;
  if (typeof matcher === 'string' || Array.isArray(matcher)) {
    return createNodeMatcher(matcher);
  }
  if (typeof matcher === 'object') {
    const cached = matcherCache.get(matcher);
    if (cached) return cached;
    const compiled = createNodeMatcher(matcher);
    matcherCache.set(matcher, compiled);
    return compiled;
  }
  return null;
}
