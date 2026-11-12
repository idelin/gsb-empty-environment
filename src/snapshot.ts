import type {
  AttributeSnapshot,
  DomSnapshot,
  NormalizedAttribute,
  NormalizedNode,
  SnapshotAttribute,
  SnapshotAttributeBag,
  SnapshotAttributeEntry,
  SnapshotNode,
} from './types.ts';

const TEMPLATE_PROVENANCE = new Set([
  'template',
  'templated',
  'static-template',
  'from-template',
  'blueprint',
]);

const BLOCKED_PROVENANCE = new Set([
  'template',
  'templated',
  'static-template',
  'from-template',
  'placeholder',
  'placeholder-value',
  'mock',
  'mocked',
  'synthetic',
  'fixture',
  'default',
  'draft',
  'unresolved',
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function scalarToString(value: SnapshotAttribute | unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (isRecord(value)) {
    const candidate = value.value ?? value.text ?? value.content;
    if (candidate !== undefined) return scalarToString(candidate);
    const current = value.currentValue ?? value.rawValue;
    if (current !== undefined) return scalarToString(current);
  }
  return '';
}

function provenanceString(value: unknown): string {
  if (typeof value === 'string') return value.toLowerCase();
  if (isRecord(value)) {
    if (value.fromTemplate === true) return 'template';
    const kind = value.kind ?? value.source ?? value.origin ?? value.type;
    if (kind !== undefined) return provenanceString(kind);
  }
  return '';
}

function provenanceParts(value: unknown): string[] {
  return provenanceString(value)
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

function provenanceFromNode(raw: SnapshotNode): string {
  return provenanceString(raw.provenance ?? raw.source ?? raw.origin);
}

function hasBlockedProvenance(value: unknown): boolean {
  return provenanceParts(value).some((part) => BLOCKED_PROVENANCE.has(part));
}

function hasTemplateProvenance(value: unknown): boolean {
  if (isRecord(value) && value.fromTemplate === true) return true;
  return provenanceParts(value).some((part) => TEMPLATE_PROVENANCE.has(part) || part === 'template');
}

function normalizeStyle(raw: SnapshotNode): Record<string, string> {
  const merged: Record<string, string> = {};
  const add = (input: string | Record<string, unknown> | undefined) => {
    if (!input) return;
    if (typeof input === 'string') {
      for (const declaration of splitDeclarations(input)) {
        const colon = declaration.indexOf(':');
        if (colon < 0) continue;
        const name = declaration.slice(0, colon).trim().toLowerCase();
        const value = declaration.slice(colon + 1).trim();
        if (name) merged[name] = value;
      }
      return;
    }
    for (const [name, value] of Object.entries(input)) {
      if (value === null || value === undefined) continue;
      merged[normalizeStyleName(name)] = String(value).trim();
    }
  };
  add(raw.style as string | Record<string, unknown> | undefined);
  const styleAttribute = collectAttributes(raw).style;
  if (isRecord(styleAttribute) && !('value' in styleAttribute) && !('text' in styleAttribute) && !('content' in styleAttribute)) {
    add(styleAttribute as Record<string, unknown>);
  } else {
    add(scalarToString(styleAttribute));
  }
  add(raw.styles as Record<string, unknown> | undefined);
  add(raw.computedStyle as string | Record<string, unknown> | undefined);
  add(raw.css as Record<string, unknown> | undefined);
  return merged;
}

function splitDeclarations(input: string): string[] {
  const declarations: string[] = [];
  let current = '';
  let depth = 0;
  let quote: string | null = null;
  for (let index = 0; index < input.length; index += 1) {
    const char = input[index];
    if (quote) {
      current += char;
      if (char === '\\') {
        index += 1;
        current += input[index] ?? '';
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
    if (char === '(') depth += 1;
    if (char === ')') depth -= 1;
    if (char === ';' && depth === 0) {
      declarations.push(current);
      current = '';
    } else {
      current += char;
    }
  }
  if (current) declarations.push(current);
  return declarations;
}

function normalizeStyleName(name: string): string {
  return name
    .replace(/([A-Z])/g, (match) => `-${match.toLowerCase()}`)
    .replace(/^-/, '')
    .toLowerCase();
}

export function styleValue(node: NormalizedNode, name: string): string {
  const normalized = normalizeStyleName(name);
  return (node.style[normalized] ?? '').toLowerCase();
}

function visibilityFlag(raw: SnapshotNode): { hidden: boolean; reason: string | null } {
  if (raw.inTemplate === true) return { hidden: false, reason: null };
  if (typeof raw.visibility === 'boolean') {
    return raw.visibility ? { hidden: false, reason: null } : { hidden: true, reason: 'visibility-false' };
  }
  if (typeof raw.visibility === 'string') {
    const value = raw.visibility.toLowerCase();
    if (value === 'hidden' || value === 'collapse' || value === 'collapsed' || value === 'none') {
      return { hidden: true, reason: `visibility-${value}` };
    }
  }
  if (isRecord(raw.visibility)) {
    const visibility = raw.visibility;
    if (visibility.visible === false) return { hidden: true, reason: 'visibility-false' };
    if (visibility.hidden === true) return { hidden: true, reason: 'visibility-hidden' };
    if (visibility.hiddenByAncestor === true) return { hidden: true, reason: 'hidden-ancestor' };
    const value = typeof visibility.value === 'string' ? visibility.value.toLowerCase() : '';
    const state = typeof visibility.state === 'string' ? visibility.state.toLowerCase() : '';
    if (value === 'hidden' || value === 'collapse' || state === 'hidden') {
      return { hidden: true, reason: 'visibility-hidden' };
    }
    const display = typeof visibility.display === 'string' ? visibility.display.toLowerCase() : '';
    if (display === 'none') return { hidden: true, reason: 'display-none' };
    const cssVisibility = typeof visibility.visibility === 'string' ? visibility.visibility.toLowerCase() : '';
    if (cssVisibility === 'hidden' || cssVisibility === 'collapse') {
      return { hidden: true, reason: `visibility-${cssVisibility}` };
    }
    if (visibility.opacity !== undefined && visibility.opacity !== '' && visibility.opacity !== null) {
      const opacity = Number(visibility.opacity);
      if (Number.isFinite(opacity) && opacity <= 0) return { hidden: true, reason: 'opacity-zero' };
    }
    if (typeof visibility.reason === 'string' && visibility.reason) {
      return { hidden: true, reason: visibility.reason };
    }
  }

  if (raw.visible === false) return { hidden: true, reason: 'visible-false' };
  if (raw.hidden === true) return { hidden: true, reason: 'hidden-flag' };
  if (raw.isVisible === false) return { hidden: true, reason: 'is-visible-false' };
  if (raw.rendered === false) return { hidden: true, reason: 'not-rendered' };
  if (raw.hiddenByAncestor === true) return { hidden: true, reason: 'hidden-ancestor' };
  if (typeof raw.display === 'string' && raw.display.toLowerCase() === 'none') {
    return { hidden: true, reason: 'display-none' };
  }
  if (raw.opacity !== undefined && raw.opacity !== null && raw.opacity !== '') {
    const rawOpacity = Number(raw.opacity);
    if (Number.isFinite(rawOpacity) && rawOpacity <= 0) return { hidden: true, reason: 'opacity-zero' };
  }

  const attributes = collectAttributes(raw);
  if ('hidden' in attributes) {
    const value = scalarToString(attributes.hidden).toLowerCase();
    if (value === '' || (value !== 'false' && value !== '0')) {
      return { hidden: true, reason: 'hidden-attribute' };
    }
  }
  if ('data-hidden' in attributes) {
    const value = scalarToString(attributes['data-hidden']).toLowerCase();
    if (value === 'true' || value === '1' || value === '') {
      return { hidden: true, reason: 'data-hidden' };
    }
  }
  if ('aria-hidden' in attributes) {
    const value = scalarToString(attributes['aria-hidden']).toLowerCase();
    if (value === 'true' || value === '1') {
      return { hidden: true, reason: 'aria-hidden' };
    }
  }
  if ('data-visible' in attributes) {
    const value = scalarToString(attributes['data-visible']).toLowerCase();
    if (value === 'false' || value === '0') {
      return { hidden: true, reason: 'data-visible-false' };
    }
  }
  if ('visible' in attributes) {
    const value = scalarToString(attributes.visible).toLowerCase();
    if (value === 'false' || value === '0') {
      return { hidden: true, reason: 'visible-false' };
    }
  }
  if ('isVisible' in attributes || 'is-visible' in attributes) {
    const value = scalarToString(attributes.isVisible ?? attributes['is-visible']).toLowerCase();
    if (value === 'false' || value === '0') {
      return { hidden: true, reason: 'is-visible-false' };
    }
  }
  if ('visibility' in attributes) {
    const value = scalarToString(attributes.visibility).toLowerCase();
    if (value === 'hidden' || value === 'collapse' || value === 'collapsed') {
      return { hidden: true, reason: `visibility-${value}` };
    }
  }
  const classTokens = scalarToString(attributes.class).toLowerCase().split(/\s+/).filter(Boolean);
  if (
    classTokens.some((token) =>
      ['hidden', 'd-none', 'display-none', 'is-hidden', 'visually-hidden', 'sr-only'].includes(token),
    )
  ) {
    return { hidden: true, reason: 'hidden-class' };
  }

  const style = normalizeStyle(raw);
  const display = (style.display ?? '').toLowerCase();
  const cssVisibility = (style.visibility ?? '').toLowerCase();
  if (display === 'none') return { hidden: true, reason: 'display-none' };
  if (cssVisibility === 'hidden' || cssVisibility === 'collapse') {
    return { hidden: true, reason: `visibility-${cssVisibility}` };
  }
  if (style.opacity !== undefined && style.opacity !== '') {
    const opacity = Number(style.opacity);
    if (Number.isFinite(opacity) && opacity <= 0) return { hidden: true, reason: 'opacity-zero' };
  }
  if ((style['content-visibility'] ?? '').toLowerCase() === 'hidden') {
    return { hidden: true, reason: 'content-visibility-hidden' };
  }

  return { hidden: false, reason: null };
}

function templateFlag(raw: SnapshotNode): boolean {
  if (raw.inTemplate === true || raw.isTemplate === true || raw.template === true || raw.fromTemplate === true) {
    return true;
  }
  if (String(raw.type ?? raw.kind ?? '').toLowerCase() === 'template') return true;
  if (hasTemplateProvenance(raw.provenance) || hasTemplateProvenance(raw.source) || hasTemplateProvenance(raw.origin)) {
    return true;
  }
  const tag = String(raw.tagName ?? raw.tag ?? raw.localName ?? '').toLowerCase();
  if (tag === 'template') return true;
  const attrs = collectAttributes(raw);
  const type = scalarToString(attrs.type).toLowerCase();
  if (type === 'text/template' || type === 'text/x-template' || type === 'text/html-template') return true;

  for (const name of [
    'data-template',
    'data-template-id',
    'data-template-name',
    'data-template-content',
    'data-is-template',
    'is-template',
  ]) {
    if (!(name in attrs)) continue;
    const value = scalarToString(attrs[name]).toLowerCase();
    if (value !== 'false' && value !== '0') return true;
  }
  const id = scalarToString(attrs.id).toLowerCase();
  const className = scalarToString(attrs.class).toLowerCase();
  const tokens = new Set(
    `${id} ${className}`
      .split(/[\s_]+/)
      .filter(Boolean),
  );
  for (const token of tokens) {
    if (
      token === 'template' ||
      token === 'tmpl' ||
      token.endsWith('-template') ||
      token.startsWith('template-') ||
      token.startsWith('tmpl-') ||
      token.endsWith('-tmpl')
    ) {
      return true;
    }
  }
  return false;
}

function attributeProvenance(
  raw: SnapshotNode,
  name: string,
  rawAttribute: SnapshotAttribute,
): { provenance: string; detail?: string; fromTemplate: boolean } {
  let source: unknown;
  if (isRecord(rawAttribute)) {
    const record = rawAttribute as AttributeSnapshot;
    source = record.source ?? record.provenance ?? record.origin;
    if (record.fromTemplate === true) source = 'template';
  }
  const perAttribute = raw.attributeSources?.[name];
  if (perAttribute !== undefined && source === undefined) source = perAttribute;
  if (source === undefined) source = raw.provenance ?? raw.source ?? raw.origin;
  const provenance = provenanceString(source) || 'dom';
  return {
    provenance,
    detail: typeof source === 'string' ? source : isRecord(source) && typeof source.kind === 'string' ? source.kind : undefined,
    fromTemplate: hasTemplateProvenance(source),
  };
}

function normalizeAttributeBag(bag: SnapshotAttributeBag | undefined): Record<string, SnapshotAttribute> {
  const result: Record<string, SnapshotAttribute> = {};
  if (!bag) return result;
  if (Array.isArray(bag)) {
    for (const entry of bag) {
      if (!isRecord(entry)) continue;
      const attribute = entry as SnapshotAttributeEntry;
      const name = String(attribute.name ?? '').toLowerCase();
      if (!name) continue;
      let value: SnapshotAttribute = attribute.value ?? attribute.text ?? attribute.content ?? '';
      if (
        (attribute.source !== undefined || attribute.provenance !== undefined || attribute.origin !== undefined || attribute.fromTemplate === true) &&
        !(isRecord(value) && (value.source !== undefined || value.provenance !== undefined || value.origin !== undefined || value.fromTemplate === true))
      ) {
        const baseValue = isRecord(value)
          ? (value.value ?? value.text ?? value.content ?? '')
          : value;
        value = {
          value: baseValue as string | number | boolean | null | undefined,
          ...(attribute.source !== undefined ? { source: attribute.source } : {}),
          ...(attribute.provenance !== undefined ? { provenance: attribute.provenance } : {}),
          ...(attribute.origin !== undefined ? { origin: attribute.origin } : {}),
          ...(attribute.fromTemplate === true ? { fromTemplate: true } : {}),
        };
      }
      result[name] = value;
    }
    return result;
  }
  for (const [name, value] of Object.entries(bag)) {
    result[name.toLowerCase()] = value;
  }
  return result;
}

function collectAttributes(raw: SnapshotNode): Record<string, SnapshotAttribute> {
  const result = {
    ...normalizeAttributeBag(raw.attrs),
    ...normalizeAttributeBag(raw.attributes),
    ...normalizeAttributeBag(raw.attribs),
  };
  if (result.class === undefined) {
    if (Array.isArray(raw.classList) && raw.classList.length) result.class = raw.classList.join(' ');
    else if (typeof raw.className === 'string') result.class = raw.className;
  }
  return result;
}

function nodeKind(raw: SnapshotNode, hasChildren: boolean): NormalizedNode['kind'] {
  const explicit = String(raw.type ?? raw.kind ?? '').toLowerCase();
  const tag = String(raw.tagName ?? raw.tag ?? raw.localName ?? '').toLowerCase();
  if (raw.nodeType === 3) return 'text';
  if (raw.nodeType === 9 || raw.nodeType === 11) return 'document';
  if (raw.nodeType === 1) return 'element';
  if (explicit === 'text' || explicit === 'textnode' || explicit === '#text' || tag === '#text') return 'text';
  if (explicit === 'document' || explicit === 'documentnode' || tag === '#document') return 'document';
  if (explicit === 'template' || tag === 'template') return 'element';
  if (tag && tag !== '#text') return 'element';
  if (collectAttributeKeys(raw).length || hasChildren) return 'element';
  if (raw.text !== undefined || raw.textContent !== undefined || raw.data !== undefined) return 'text';
  return 'other';
}

function collectAttributeKeys(raw: SnapshotNode): string[] {
  return Object.keys(collectAttributes(raw));
}

function ownText(raw: SnapshotNode, kind: NormalizedNode['kind'], hasChildren: boolean): string | null {
  if (kind === 'text') {
    if (typeof raw.text === 'string') return raw.text;
    if (typeof raw.textContent === 'string') return raw.textContent;
    if (typeof raw.data === 'string') return raw.data;
    if (typeof raw.nodeValue === 'string') return raw.nodeValue;
    if (typeof raw.value === 'string' || typeof raw.value === 'number') return String(raw.value);
    return null;
  }
  if (kind === 'element' && !hasChildren) {
    if (typeof raw.text === 'string') return raw.text;
    if (typeof raw.textContent === 'string') return raw.textContent;
  }
  return null;
}

function rawChildren(raw: SnapshotNode): SnapshotNode[] {
  const children: SnapshotNode[] = [];
  const primary = raw.children ?? raw.childNodes ?? raw.nodes;
  if (Array.isArray(primary)) children.push(...primary);
  const content = raw.content ?? raw.templateContent;
  if (Array.isArray(content)) {
    children.push(...content);
  } else if (isRecord(content)) {
    children.push(content as SnapshotNode);
  }
  return children.filter((child) => isRecord(child) && !Array.isArray(child)) as SnapshotNode[];
}

function pickRawRoot(input: DomSnapshot | SnapshotNode | SnapshotNode[]): SnapshotNode {
  if (Array.isArray(input)) {
    return { id: '__document__', type: 'document', children: input };
  }
  if (input.root && isRecord(input.root)) return input.root as SnapshotNode;
  if (input.document && isRecord(input.document)) return input.document as SnapshotNode;
  if (input.html && isRecord(input.html)) return input.html as SnapshotNode;

  const looksLikeWrapper =
    Array.isArray(input.nodes) &&
    input.tagName === undefined &&
    input.tag === undefined &&
    input.kind === undefined &&
    input.type === undefined &&
    input.text === undefined &&
    input.textContent === undefined;
  if (looksLikeWrapper) {
    const nodes = input.nodes as SnapshotNode[];
    if (nodes.every((node) => typeof node.parentId === 'string')) {
      return buildFlatRoot(nodes);
    }
    return { id: '__document__', type: 'document', children: nodes };
  }
  return input as SnapshotNode;
}

function buildFlatRoot(nodes: SnapshotNode[]): SnapshotNode {
  const copies = new Map<string, SnapshotNode>();
  for (const node of nodes) {
    const id = String(node.id ?? node.nodeId ?? '');
    if (!id) continue;
    copies.set(id, { ...node, children: [] });
  }
  const roots: SnapshotNode[] = [];
  for (const node of nodes) {
    const id = String(node.id ?? node.nodeId ?? '');
    const copy = copies.get(id);
    if (!copy) continue;
    const parentId = typeof node.parentId === 'string' ? node.parentId : '';
    const parent = parentId ? copies.get(parentId) : undefined;
    if (parent) parent.children!.push(copy);
    else roots.push(copy);
  }
  return { id: '__document__', type: 'document', children: roots };
}

export function normalizeSnapshot(input: DomSnapshot | SnapshotNode | SnapshotNode[]): NormalizedNode {
  const rawRoot = pickRawRoot(input);
  const usedIds = new Set<string>();
  const seenRawNodes = new WeakSet<object>();

  const visit = (raw: SnapshotNode, parent: NormalizedNode | null, path: number[]): NormalizedNode => {
    if (isRecord(raw)) {
      if (seenRawNodes.has(raw)) {
        throw new Error(`Snapshot contains a cycle or a reused node object at ${path.join('.')}`);
      }
      seenRawNodes.add(raw);
    }

    const childrenRaw = rawChildren(raw);
    const kind = nodeKind(raw, childrenRaw.length > 0);
    const tagName = kind === 'text'
      ? '#text'
      : String(raw.tagName ?? raw.tag ?? raw.localName ?? (kind === 'document' ? '#document' : '')).toLowerCase();
    const attrsRaw = collectAttributes(raw);
    if (kind === 'element' && attrsRaw.id === undefined && (typeof raw.id === 'string' || typeof raw.nodeId === 'string')) {
      attrsRaw.id = String(raw.id ?? raw.nodeId ?? '');
    }
    const attributes: NormalizedAttribute[] = [];
    for (const [name, value] of Object.entries(attrsRaw)) {
      const normalizedName = name.toLowerCase();
      const provenance = attributeProvenance(raw, normalizedName, value);
      attributes.push({
        name: normalizedName,
        value: scalarToString(value),
        raw: value,
        provenance: provenance.provenance,
        provenanceDetail: provenance.detail,
        fromTemplate: provenance.fromTemplate,
      });
    }
    const attributeMap: Record<string, NormalizedAttribute> = {};
    for (const entry of attributes) attributeMap[entry.name] = entry;

    const baseId = String(raw.id ?? raw.nodeId ?? `#${path.join('.') || '0'}`);
    let id = baseId;
    if (usedIds.has(id)) {
      id = `${baseId}@${path.join('.') || '0'}`;
      let suffix = 1;
      while (usedIds.has(id)) {
        id = `${baseId}@${path.join('.') || '0'}#${suffix}`;
        suffix += 1;
      }
    }
    usedIds.add(id);

    const visibility = visibilityFlag(raw);
    const inTemplate = templateFlag(raw);
    const text = ownText(raw, kind, childrenRaw.length > 0);
    const node: NormalizedNode = {
      id,
      rawId: typeof raw.id === 'string' ? raw.id : typeof raw.nodeId === 'string' ? raw.nodeId : undefined,
      kind,
      tagName,
      attributes,
      attributeMap,
      text,
      children: [],
      parent,
      path,
      depth: path.length,
      ownHidden: visibility.hidden,
      ownHiddenReason: visibility.reason,
      hiddenByAncestor: raw.hiddenByAncestor === true,
      inTemplate,
      provenance: provenanceFromNode(raw),
      style: normalizeStyle(raw),
      raw,
    };
    node.children = childrenRaw.map((child, index) => visit(child, node, [...path, index]));
    return node;
  };

  return visit(rawRoot, null, []);
}

export function nodeOwnText(node: NormalizedNode): string | null {
  return node.text;
}

export function getAttribute(node: NormalizedNode, name: string): string | undefined {
  return node.attributeMap[name.toLowerCase()]?.value;
}

export function hasAttribute(node: NormalizedNode, name: string): boolean {
  return Boolean(node.attributeMap[name.toLowerCase()]);
}

export function isStruck(node: NormalizedNode): boolean {
  if (node.kind === 'element' && ['s', 'strike', 'del'].includes(node.tagName)) return true;
  if (hasAttribute(node, 'data-strikethrough') || hasAttribute(node, 'strikethrough')) {
    const value = getAttribute(node, 'data-strikethrough') ?? getAttribute(node, 'strikethrough') ?? '';
    if (value === '' || value.toLowerCase() !== 'false') return true;
  }
  const decoration = [
    typeof raw.textDecorationLine === 'string' ? raw.textDecorationLine.toLowerCase() : '',
    typeof raw.textDecoration === 'string' ? raw.textDecoration.toLowerCase() : '',
    styleValue(node, 'text-decoration-line'),
    styleValue(node, 'text-decoration'),
    styleValue(node, '-webkit-text-decoration'),
    styleValue(node, '-webkit-text-decoration-line'),
  ].join(' ');
  if (decoration.includes('line-through')) return true;

  const className = (getAttribute(node, 'class') ?? '').toLowerCase();
  const tokens = className.split(/\s+/).filter(Boolean);
  const strikeTokens = new Set([
    'strikethrough',
    'strike-through',
    'line-through',
    'linethrough',
    'struck',
    'old-price',
    'original-price',
    'was-price',
    'compare-at-price',
    'list-price',
    'price-old',
    'price-original',
  ]);
  if (tokens.some((token) => strikeTokens.has(token))) return true;
  for (const name of ['data-old-price', 'data-original-price', 'data-list-price', 'data-compare-at-price']) {
    const value = getAttribute(node, name);
    if (value !== undefined && value.toLowerCase() !== 'false') return true;
  }
  return false;
}

export function findHiddenAncestor(node: NormalizedNode): { node: NormalizedNode; reason: string } | null {
  let current: NormalizedNode | null = node;
  while (current) {
    if (current.ownHidden) {
      return {
        node: current,
        reason: current.ownHiddenReason ?? (current === node ? 'hidden' : 'hidden-ancestor'),
      };
    }
    if (current.hiddenByAncestor && current.parent) {
      return { node: current.parent, reason: 'hidden-ancestor' };
    }
    current = current.parent;
  }
  return null;
}

export function findTemplateAncestor(node: NormalizedNode): NormalizedNode | null {
  let current: NormalizedNode | null = node;
  while (current) {
    if (current.inTemplate) return current;
    current = current.parent;
  }
  return null;
}

export function isBlockedAttributeProvenance(attribute: NormalizedAttribute): boolean {
  return attribute.fromTemplate || hasBlockedProvenance(attribute.provenance);
}

export function isBlockedTextProvenance(node: NormalizedNode): boolean {
  return hasTemplateProvenance(node.provenance) || hasBlockedProvenance(node.provenance);
}

export function assertNodeKind(raw: unknown): raw is SnapshotNode {
  return isRecord(raw);
}
