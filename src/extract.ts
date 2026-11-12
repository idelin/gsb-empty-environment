import type {
  AmountIssue,
  CurrencyEvidence,
  DomSnapshot,
  ExtractionIssue,
  ExtractionOptions,
  NormalizedNode,
  PriceCandidate,
  PriceExtractionResult,
  PricingRules,
  ProductExtraction,
  ResolvedPrice,
  SnapshotNode,
  TraceSpan,
} from './types.ts';
import { findCurrencyMentions, expectedCurrency, currencyCodesForToken, mergeCurrencyCodes } from './currency.ts';
import type { ExpectedCurrency } from './currency.ts';
import { normalizeNumberFormat, scanAmountText } from './number.ts';
import type { NormalizedNumberFormat } from './number.ts';
import { compileMatcher } from './selector.ts';
import {
  findHiddenAncestor,
  findTemplateAncestor,
  getAttribute,
  isBlockedAttributeProvenance,
  isBlockedTextProvenance,
  isStruck,
  normalizeSnapshot,
} from './snapshot.ts';

const DEFAULT_PRICE_ATTRIBUTES = [
  'data-price',
  'data-price-value',
  'data-current-price',
  'data-sale-price',
  'data-final-price',
  'data-offer-price',
  'data-amount',
  'price',
  'content',
  'value',
  'data-value',
];

const CURRENCY_ATTRIBUTES = [
  'data-currency',
  'data-currency-code',
  'data-currency-symbol',
  'data-price-currency',
  'pricecurrency',
  'currency',
];

const PLACEHOLDER_PATTERNS: RegExp[] = [
  /\{\{[^}]+\}\}/,
  /\$\{[^}]+\}/,
  /<%[^%]+%>/,
  /\[\[[^\]]+\]\]/,
  /__[A-Za-z0-9_.-]+__/,
  /\{[A-Za-z0-9_.-]+\}/,
  /\b(?:TBD|TODO|N\/A|placeholder|placeholder-value|dummy price|sample price)\b/i,
  /\bxxxx+\b/i,
];

type CompiledMatcher = (node: NormalizedNode) => boolean;

interface Surface {
  node: NormalizedNode;
  fieldNode: NormalizedNode;
  text: string;
  source: 'text' | 'attribute';
  attribute?: string;
}

interface CurrencyEvidenceEntry {
  codes: string[];
  source: 'text' | 'attribute' | 'config';
  nodeId?: string;
  start?: number;
  end?: number;
  raw?: string;
  attribute?: string;
  explicit: boolean;
}

interface CurrencyResolution {
  code?: string;
  reasons: string[];
  evidence: CurrencyEvidence[];
  trace?: TraceSpan;
  entries: CurrencyEvidenceEntry[];
}

interface CandidateContext {
  scope: NormalizedNode;
  scopeNodes: NormalizedNode[];
  scopeCurrency: CurrencyEvidenceEntry[];
  markerIds: string[];
  originalMarkerIds: string[];
  hidden: ReturnType<typeof findHiddenAncestor>;
  template: NormalizedNode | null;
  struck: NormalizedNode | null;
  placeholder: boolean;
  provenanceBlocked: boolean;
}

export class PriceConfigError extends Error {
  issues: ExtractionIssue[];

  constructor(issues: ExtractionIssue[]) {
    super(`Invalid price extraction config: ${issues.map((issue) => issue.code).join(', ')}`);
    this.name = 'PriceConfigError';
    this.issues = issues;
  }
}

function toArray<T>(value: T | T[] | undefined): T[] {
  if (value === undefined) return [];
  return Array.isArray(value) ? value : [value];
}

function nearestElement(node: NormalizedNode): NormalizedNode | null {
  let current: NormalizedNode | null = node;
  while (current) {
    if (current.kind === 'element') return current;
    current = current.parent;
  }
  return null;
}

function ancestorsWithinScope(node: NormalizedNode, scope: NormalizedNode): NormalizedNode[] {
  const chain: NormalizedNode[] = [];
  let current: NormalizedNode | null = node;
  while (current) {
    chain.push(current);
    if (current.id === scope.id) break;
    current = current.parent;
  }
  return chain;
}

function ancestorsToRoot(node: NormalizedNode): NormalizedNode[] {
  const chain: NormalizedNode[] = [];
  let current: NormalizedNode | null = node;
  while (current) {
    chain.push(current);
    current = current.parent;
  }
  return chain;
}

function textOfSurface(node: NormalizedNode): string | null {
  if (node.kind === 'text') return node.text;
  if (node.kind === 'element' && node.children.length === 0) return node.text;
  return null;
}

function surfacesForNode(node: NormalizedNode): Surface[] {
  const surfaces: Surface[] = [];
  const text = textOfSurface(node);
  if (text !== null) {
    const fieldNode = node.kind === 'element' ? node : nearestElement(node.parent ?? node);
    if (fieldNode) surfaces.push({ node, fieldNode, text, source: 'text' });
  }

  if (node.kind !== 'element') return surfaces;
  return surfaces;
}

function isPlaceholderText(text: string): boolean {
  return PLACEHOLDER_PATTERNS.some((pattern) => pattern.test(text));
}

function hasPlaceholderAttribute(fieldNode: NormalizedNode): boolean {
  const value = getAttribute(fieldNode, 'data-placeholder') ?? getAttribute(fieldNode, 'placeholder');
  return value !== undefined && value.toLowerCase() !== 'false' && value !== '0';
}

function mergeReasons(target: string[], values: string[]): void {
  for (const value of values) {
    if (!target.includes(value)) target.push(value);
  }
}

function traceFor(surface: Surface, start: number, end: number): TraceSpan {
  const text = surface.text.slice(start, end);
  return {
    nodeId: surface.node.id,
    start,
    end,
    text,
    source: surface.source,
    ...(surface.attribute ? { attribute: surface.attribute } : {}),
  };
}

function makeCandidate(
  surface: Surface,
  scopeId: string,
  start: number,
  end: number,
  text: string,
  context: CandidateContext,
  extraReasons: string[],
  issues: AmountIssue[] = [],
): PriceCandidate {
  const baseReasons: string[] = [];
  if (context.hidden) {
    baseReasons.push('hidden');
    if (context.hidden.node.id !== surface.fieldNode.id) baseReasons.push('hidden-ancestor');
    if (context.hidden.reason && context.hidden.reason !== 'hidden') baseReasons.push(context.hidden.reason);
  }
  if (context.template) baseReasons.push('template');
  if (context.struck) baseReasons.push('strikethrough');
  if (context.originalMarkerIds.length) baseReasons.push('original-price');
  if (context.placeholder) baseReasons.push('placeholder');
  if (context.provenanceBlocked) baseReasons.push('attribute-provenance');
  mergeReasons(baseReasons, extraReasons);

  const span = traceFor(surface, start, end);
  return {
    scopeId,
    nodeId: surface.node.id,
    start,
    end,
    text,
    amountText: text,
    source: surface.source,
    ...(surface.attribute ? { attribute: surface.attribute } : {}),
    fullText: surface.text,
    eligible: false,
    reasons: baseReasons,
    markerIds: context.markerIds,
    originalMarkerIds: context.originalMarkerIds,
    fieldNodeId: surface.fieldNode.id,
    issues,
    span,
  };
}

function tokenMentions(text: string, start: number, end: number) {
  const mentions = findCurrencyMentions(text);
  const isCurrencyGap = (from: number, to: number): boolean => {
    let index = from;
    while (index < to) {
      const mention = mentions.find((entry) => entry.start === index && entry.end <= to);
      if (mention) {
        index = mention.end;
        continue;
      }
      if (/^[\s:：/\-–—~()]$/.test(text[index])) {
        index += 1;
        continue;
      }
      return false;
    }
    return true;
  };
  return mentions.filter((mention) => {
    if (mention.end <= start) {
      return isCurrencyGap(mention.end, start);
    }
    if (mention.start >= end) {
      return isCurrencyGap(end, mention.start);
    }
    return false;
  });
}

function attributeCurrencyEntries(fieldNode: NormalizedNode, scope: NormalizedNode): CurrencyEvidenceEntry[] {
  const chain = ancestorsWithinScope(fieldNode, scope);
  for (const node of chain) {
    if (node.kind !== 'element') continue;
    const entries: CurrencyEvidenceEntry[] = [];
    for (const name of CURRENCY_ATTRIBUTES) {
      const attribute = node.attributeMap[name];
      if (!attribute || isBlockedAttributeProvenance(attribute)) continue;
      entries.push(...currencyEntriesFromAttribute(node, attribute.name, attribute.value));
    }
    const itemprop = (getAttribute(node, 'itemprop') ?? '').toLowerCase();
    const property = (getAttribute(node, 'property') ?? '').toLowerCase();
    const attribute = node.attributeMap.content;
    if (attribute && !isBlockedAttributeProvenance(attribute) && (itemprop.includes('pricecurrency') || property.includes('price:currency'))) {
      entries.push(...currencyEntriesFromAttribute(node, 'content', attribute.value));
    }
    if (entries.length) return entries;
  }
  return [];
}

function currencyEntriesFromAttribute(node: NormalizedNode, attribute: string, value: string): CurrencyEvidenceEntry[] {
  const mentions = findCurrencyMentions(value);
  if (mentions.length) {
    return mentions.map((mention) => ({
      codes: mention.codes,
      source: 'attribute',
      nodeId: node.id,
      start: mention.start,
      end: mention.end,
      raw: mention.text,
      attribute,
      explicit: mention.codes.length === 1,
    }));
  }
  const codes = currencyCodesForToken(value.trim());
  if (!codes.length) return [];
  return [{
    codes,
    source: 'attribute',
    nodeId: node.id,
    start: 0,
    end: value.length,
    raw: value,
    attribute,
    explicit: codes.length === 1,
  }];
}

function scopeCurrencyEntries(
  scopeNodes: NormalizedNode[],
  scope: NormalizedNode,
  format: NormalizedNumberFormat,
  strike: (node: NormalizedNode) => boolean,
  hidden: (node: NormalizedNode) => boolean,
  template: (node: NormalizedNode) => boolean,
  original: (node: NormalizedNode) => boolean,
): CurrencyEvidenceEntry[] {
  const entries: CurrencyEvidenceEntry[] = [];
  for (const node of scopeNodes) {
    const text = textOfSurface(node);
    if (text === null || !text.trim()) continue;
    const scan = scanAmountText(text, format);
    if (scan.amounts.length || scan.invalid.length) continue;
    const fieldNode = node.kind === 'element' ? node : nearestElement(node.parent ?? node);
    if (!fieldNode) continue;
    if (hidden(fieldNode) || template(fieldNode) || strike(fieldNode) || isPlaceholderText(text)) continue;
    if (ancestorsWithinScope(fieldNode, scope).some((ancestor) => original(ancestor))) continue;
    if (isBlockedTextProvenance(node) || isBlockedTextProvenance(fieldNode)) continue;
    for (const mention of findCurrencyMentions(text)) {
      entries.push({
        codes: mention.codes,
        source: 'text',
        nodeId: node.id,
        start: mention.start,
        end: mention.end,
        raw: mention.text,
        explicit: mention.codes.length === 1,
      });
    }
  }
  return entries;
}

function scopeAttributeCurrencyEntries(
  scopeNodes: NormalizedNode[],
  scope: NormalizedNode,
  hidden: (node: NormalizedNode) => boolean,
  template: (node: NormalizedNode) => boolean,
  strike: (node: NormalizedNode) => boolean,
  original: (node: NormalizedNode) => boolean,
): CurrencyEvidenceEntry[] {
  const entries: CurrencyEvidenceEntry[] = [];
  for (const node of scopeNodes) {
    if (node.kind !== 'element') continue;
    if (hidden(node) || template(node) || strike(node)) continue;
    if (ancestorsWithinScope(node, scope).some((ancestor) => original(ancestor))) continue;
    for (const name of CURRENCY_ATTRIBUTES) {
      const attribute = node.attributeMap[name];
      if (!attribute || isBlockedAttributeProvenance(attribute)) continue;
      entries.push(...currencyEntriesFromAttribute(node, attribute.name, attribute.value));
    }
    const itemprop = (getAttribute(node, 'itemprop') ?? '').toLowerCase();
    const property = (getAttribute(node, 'property') ?? '').toLowerCase();
    const attribute = node.attributeMap.content;
    if (attribute && !isBlockedAttributeProvenance(attribute) && (itemprop.includes('pricecurrency') || property.includes('price:currency'))) {
      entries.push(...currencyEntriesFromAttribute(node, 'content', attribute.value));
    }
  }
  return entries;
}

function resolveCurrency(
  entries: CurrencyEvidenceEntry[],
  expected: ExpectedCurrency,
  requireCurrency: boolean,
): CurrencyResolution {
  const reasons: string[] = [];
  const explicitCodes = mergeCurrencyCodes(
    entries.filter((entry) => entry.explicit && entry.codes.length === 1).map((entry) => entry.codes[0]),
  );
  let code: string | undefined;

  if (explicitCodes.length > 1) {
    reasons.push('currency-ambiguous');
  } else if (explicitCodes.length === 1) {
    const explicit = explicitCodes[0];
    if (expected.configured && expected.codes.length && !expected.codes.includes(explicit)) {
      reasons.push('currency-mismatch');
    } else {
      code = explicit;
    }
  } else {
    const allCodes = mergeCurrencyCodes(entries.flatMap((entry) => entry.codes));
    if (expected.configured && expected.codes.length) {
      const matched = allCodes.filter((candidate) => expected.codes.includes(candidate));
      if (!matched.length && allCodes.length) {
        reasons.push('currency-mismatch');
      } else if (matched.length === 1) {
        code = matched[0];
      } else if (matched.length > 1) {
        reasons.push('currency-ambiguous');
      }
    } else if (allCodes.length === 1) {
      code = allCodes[0];
    } else if (allCodes.length > 1) {
      reasons.push('currency-ambiguous');
    }
  }

  if (!code && !reasons.length) {
    if (expected.primary) {
      code = expected.primary;
      entries.push({ codes: [expected.primary], source: 'config', explicit: true });
    } else if (expected.configured && expected.ambiguous) {
      reasons.push('currency-ambiguous');
    } else if (requireCurrency) {
      reasons.push('missing-currency');
    } else {
      code = 'UNKNOWN';
    }
  }

  const evidence: CurrencyEvidence[] = entries.map((entry) => ({
    code: code && (entry.codes.includes(code) || entry.source === 'config') ? code : (entry.codes[0] ?? 'UNKNOWN'),
    source: entry.source,
    ...(entry.nodeId !== undefined && entry.start !== undefined && entry.end !== undefined
      ? {
          trace: {
            nodeId: entry.nodeId,
            start: entry.start,
            end: entry.end,
            text: entry.raw ?? '',
            source: entry.source === 'attribute' ? 'attribute' as const : 'text' as const,
            ...(entry.attribute ? { attribute: entry.attribute } : {}),
          },
        }
      : {}),
    ...(entry.raw !== undefined ? { raw: entry.raw } : {}),
    ...(entry.codes.length > 1 ? { ambiguousCodes: entry.codes } : {}),
  }));
  const preferredEntry =
    entries.find((entry) => entry.nodeId !== undefined && entry.explicit && code !== undefined && entry.codes.includes(code)) ??
    entries.find((entry) => entry.nodeId !== undefined);
  const trace = preferredEntry && preferredEntry.nodeId !== undefined && preferredEntry.start !== undefined && preferredEntry.end !== undefined
    ? {
        nodeId: preferredEntry.nodeId,
        start: preferredEntry.start,
        end: preferredEntry.end,
        text: preferredEntry.raw ?? '',
        source: preferredEntry.source === 'attribute' ? 'attribute' as const : 'text' as const,
        ...(preferredEntry.attribute ? { attribute: preferredEntry.attribute } : {}),
      }
    : undefined;
  return { code, reasons, evidence, trace, entries };
}

function currencyForCandidate(
  surface: Surface,
  start: number,
  end: number,
  scope: NormalizedNode,
  scopeCurrency: CurrencyEvidenceEntry[],
  scopeAttributeCurrency: CurrencyEvidenceEntry[],
  expected: ExpectedCurrency,
  requireCurrency: boolean,
): CurrencyResolution {
  const direct: CurrencyEvidenceEntry[] = tokenMentions(surface.text, start, end).map((mention) => ({
    codes: mention.codes,
    source: 'text',
    nodeId: surface.node.id,
    start: mention.start,
    end: mention.end,
    raw: mention.text,
    explicit: mention.codes.length === 1,
  }));
  const attributes = attributeCurrencyEntries(surface.fieldNode, scope);
  const entries = [...direct, ...attributes];
  if (!entries.some((entry) => entry.explicit)) entries.push(...scopeAttributeCurrency);
  if (!entries.length) entries.push(...scopeCurrency);
  return resolveCurrency(entries, expected, requireCurrency);
}

function productScopeNodes(root: NormalizedNode, scopeMatcher: CompiledMatcher): NormalizedNode[] {
  const scopes: NormalizedNode[] = [];
  const walk = (node: NormalizedNode) => {
    if (scopeMatcher(node)) scopes.push(node);
    for (const child of node.children) walk(child);
  };
  walk(root);
  return scopes;
}

function buildOwnership(
  root: NormalizedNode,
  scopes: NormalizedNode[],
): { ownerById: Map<string, string | null>; nodesByScope: Map<string, NormalizedNode[]> } {
  const scopeIds = new Set(scopes.map((scope) => scope.id));
  const ownerById = new Map<string, string | null>();
  const nodesByScope = new Map<string, NormalizedNode[]>();
  for (const scope of scopes) nodesByScope.set(scope.id, []);

  const walk = (node: NormalizedNode, owner: string | null) => {
    const current = scopeIds.has(node.id) ? node.id : owner;
    ownerById.set(node.id, current);
    if (current) nodesByScope.get(current)!.push(node);
    for (const child of node.children) walk(child, current);
  };
  walk(root, null);
  return { ownerById, nodesByScope };
}

function attributeSurface(attributes: string[], node: NormalizedNode): Surface[] {
  const surfaces: Surface[] = [];
  const itemprop = (getAttribute(node, 'itemprop') ?? '').toLowerCase();
  const property = (getAttribute(node, 'property') ?? '').toLowerCase();
  const names = new Set(attributes);
  for (const attribute of node.attributes) {
    if (
      /price|amount/i.test(attribute.name) &&
      !/(currency|type|label|sku|code|id)/i.test(attribute.name)
    ) {
      names.add(attribute.name);
    }
  }
  for (const name of names) {
    const attribute = node.attributeMap[name.toLowerCase()];
    if (!attribute) continue;
    if (name === 'content') {
      const priceContext = itemprop.includes('price') || property.includes('price') || property.includes('offer');
      if (!priceContext) continue;
    }
    if (!/\d/.test(attribute.value)) continue;
    surfaces.push({
      node,
      fieldNode: node,
      text: attribute.value,
      source: 'attribute',
      attribute: attribute.name,
    });
  }
  return surfaces;
}

function uniquePriceAttributes(rules: PricingRules): string[] {
  const configured = rules.priceAttributes ?? rules.amountAttributes ?? rules.priceAttribute ?? rules.amountAttribute;
  if (configured !== undefined) {
    return Array.from(new Set(toArray(configured).map((name) => name.toLowerCase())));
  }
  const result = [...DEFAULT_PRICE_ATTRIBUTES];
  for (const name of toArray(rules.currentPriceAttribute ?? rules.currentPriceAttributes)) {
    if (!result.includes(name.toLowerCase())) result.push(name.toLowerCase());
  }
  return result;
}

function isNonNegativeFinite(value: number): boolean {
  return Number.isFinite(value) && value >= 0;
}

export function extractPrices(
  snapshot: DomSnapshot | SnapshotNode | SnapshotNode[],
  rules: PricingRules,
  options: ExtractionOptions = {},
): PriceExtractionResult {
  const issues: ExtractionIssue[] = [];
  const configIssues: ExtractionIssue[] = [];
  const addIssue = (issue: ExtractionIssue) => {
    if (!issues.some((existing) => existing.code === issue.code && existing.nodeId === issue.nodeId && existing.message === issue.message)) {
      issues.push(issue);
    }
  };
  const addConfigIssue = (issue: ExtractionIssue) => {
    configIssues.push(issue);
    addIssue(issue);
  };

  let root: NormalizedNode;
  try {
    root = normalizeSnapshot(snapshot);
  } catch (error) {
    addConfigIssue({
      code: 'invalid-snapshot',
      severity: 'error',
      message: error instanceof Error ? error.message : String(error),
    });
    const result: PriceExtractionResult = { prices: [], products: [], candidates: [], ambiguous: [], issues };
    if (options.strict) throw new PriceConfigError(configIssues);
    return result;
  }

  const numberFormatResult = normalizeNumberFormat(rules);
  for (const issue of numberFormatResult.issues) {
    addConfigIssue({ code: issue.code, severity: issue.severity, message: issue.message });
  }
  const format = numberFormatResult.format;

  const expected = expectedCurrency(rules);
  if (rules.currency !== undefined && !expected.configured) {
    addConfigIssue({
      code: 'config-unrecognized-currency',
      severity: 'error',
      message: 'The configured currency is not a recognized ISO code or symbol.',
    });
  }
  if (expected.ambiguous && rules.currency !== undefined && !expected.primary) {
    addIssue({
      code: 'config-ambiguous-currency-symbol',
      severity: 'warning',
      message: 'The configured currency symbol denotes multiple ISO codes; ambiguous values will be returned as candidates unless page evidence or rules.codes disambiguates.',
    });
  }

  const scopeRule =
    rules.productScope ??
    rules.scope ??
    rules.scopeSelector ??
    rules.productScopeSelector ??
    rules.productCard ??
    rules.productSelector;
  if (scopeRule === undefined || scopeRule === null) {
    addConfigIssue({
      code: 'config-missing-product-scope',
      severity: 'error',
      message: 'Site config must describe the product scope selector.',
    });
  }
  const currentRule =
    rules.currentPriceMarker ??
    rules.currentPrice ??
    rules.priceMarker ??
    rules.currentPriceSelector ??
    rules.priceSelector;
  const configuredCurrentAttributes = toArray(rules.currentPriceAttribute ?? rules.currentPriceAttributes);
  if (currentRule === undefined && !configuredCurrentAttributes.length) {
    addConfigIssue({
      code: 'config-missing-current-price-marker',
      severity: 'error',
      message: 'Site config must describe the current-price marker.',
    });
  }
  const originalRule =
    rules.originalPriceMarker ??
    rules.originalPrice ??
    rules.oldPriceMarker ??
    rules.originalPriceSelector;

  const matcherErrors = new Set<string>();
  const safeMatch = (matcher: CompiledMatcher | null, node: NormalizedNode, code: string): boolean => {
    if (!matcher) return false;
    try {
      return matcher(node);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (!matcherErrors.has(message)) {
        matcherErrors.add(message);
        addConfigIssue({ code, severity: 'error', message });
      }
      return false;
    }
  };

  let scopeMatcher: CompiledMatcher | null = null;
  let currentMatcher: CompiledMatcher | null = null;
  let originalMatcher: CompiledMatcher | null = null;
  try {
    scopeMatcher = compileMatcher(scopeRule as never);
    currentMatcher = compileMatcher(currentRule as never);
    originalMatcher = compileMatcher(originalRule as never);
  } catch (error) {
    addConfigIssue({
      code: 'config-invalid-selector',
      severity: 'error',
      message: error instanceof Error ? error.message : String(error),
    });
  }

  if (options.strict && configIssues.some((issue) => issue.severity === 'error')) {
    throw new PriceConfigError(configIssues);
  }

  if (!scopeMatcher) {
    return { prices: [], products: [], candidates: [], ambiguous: [], issues };
  }

  const scopes = productScopeNodes(root, (node) => safeMatch(scopeMatcher, node, 'config-invalid-scope-selector'));
  if (!scopes.length && scopeRule !== undefined) {
    addIssue({
      code: 'no-product-scopes',
      severity: 'warning',
      message: 'No product scope matched the configured selector.',
    });
  }
  const { nodesByScope } = buildOwnership(root, scopes);
  const currentPriceAttributes = configuredCurrentAttributes.map((name) => name.toLowerCase());
  const priceAttributes = uniquePriceAttributes(rules);
  const requireCurrentMarker = rules.requireCurrentMarker !== false;
  const requireCurrency = rules.requireCurrency !== false;
  const includeRejected = options.includeRejectedCandidates ?? rules.includeRejectedCandidates ?? true;
  const dedupeIdenticalAmounts = options.dedupeIdenticalAmounts !== false;

  const allCandidates: PriceCandidate[] = [];
  const allAmbiguous: PriceCandidate[] = [];
  const products: ProductExtraction[] = [];
  const prices: ResolvedPrice[] = [];

  const strikeWithin = (node: NormalizedNode) => ancestorsToRoot(node).some((ancestor) => isStruck(ancestor));
  const hiddenWithin = (node: NormalizedNode) => findHiddenAncestor(node);
  const templateWithin = (node: NormalizedNode) => findTemplateAncestor(node);

  for (const scope of scopes) {
    const scopeNodes = nodesByScope.get(scope.id) ?? [];
    const scopeCurrency = scopeCurrencyEntries(
      scopeNodes,
      scope,
      format,
      strikeWithin,
      (node) => Boolean(hiddenWithin(node)),
      (node) => Boolean(templateWithin(node)),
      (node) => safeMatch(originalMatcher, node, 'config-invalid-original-marker'),
    );
    const scopeAttributeCurrency = scopeAttributeCurrencyEntries(
      scopeNodes,
      scope,
      (node) => Boolean(hiddenWithin(node)),
      (node) => Boolean(templateWithin(node)),
      strikeWithin,
      (node) => safeMatch(originalMatcher, node, 'config-invalid-original-marker'),
    );
    const candidates: PriceCandidate[] = [];

    for (const node of scopeNodes) {
      if (node.kind === 'document') continue;
      const surfaces = surfacesForNode(node);
      const attributes = node.kind === 'element' ? attributeSurface(priceAttributes, node) : [];
      for (const surface of [...surfaces, ...attributes]) {
        const fieldNode = surface.fieldNode;
        if (!fieldNode) continue;
        const chain = ancestorsWithinScope(fieldNode, scope);
        const markerIds: string[] = [];
        for (const ancestor of chain) {
          if (safeMatch(currentMatcher, ancestor, 'config-invalid-current-marker')) markerIds.push(ancestor.id);
        }
        if (!markerIds.length) {
          for (const ancestor of chain) {
            const markerAttribute = currentPriceAttributes
              .map((name) => ancestor.attributeMap[name])
              .find((attribute) => {
                if (!attribute) return false;
                const value = attribute.value.toLowerCase();
                return value !== 'false' && value !== '0';
              });
            if (markerAttribute) {
              markerIds.push(ancestor.id);
              break;
            }
          }
        }
        const originalMarkerIds: string[] = [];
        for (const ancestor of chain) {
          if (safeMatch(originalMatcher, ancestor, 'config-invalid-original-marker')) originalMarkerIds.push(ancestor.id);
        }
        const struckAncestor =
          ancestorsToRoot(fieldNode).find((ancestor) => isStruck(ancestor)) ??
          (isStruck(node) ? node : null);
        const hidden = hiddenWithin(node) ?? hiddenWithin(fieldNode);
        const template = (node.inTemplate ? node : null) ?? templateWithin(fieldNode);
        const placeholder = isPlaceholderText(surface.text) || hasPlaceholderAttribute(fieldNode);
        const fieldAttribute = surface.attribute
          ? fieldNode.attributeMap[surface.attribute]
          : undefined;
        const provenanceBlocked =
          isBlockedTextProvenance(node) ||
          isBlockedTextProvenance(fieldNode) ||
          (fieldAttribute !== undefined && isBlockedAttributeProvenance(fieldAttribute));
        const context: CandidateContext = {
          scope,
          scopeNodes,
          scopeCurrency,
          markerIds,
          originalMarkerIds,
          hidden,
          template,
          struck: struckAncestor,
          placeholder,
          provenanceBlocked,
        };

        const scan = scanAmountText(surface.text, format);
        const mentionPresence = findCurrencyMentions(surface.text).length > 0;
        const hasContext =
          markerIds.length > 0 ||
          originalMarkerIds.length > 0 ||
          Boolean(struckAncestor) ||
          Boolean(hidden) ||
          Boolean(template) ||
          surface.source === 'attribute' ||
          mentionPresence;
        const amountTokens: Array<{ text: string; start: number; end: number; value?: number; usedDecimal: boolean; usedGrouping: boolean; issue?: AmountIssue }> = [
          ...scan.amounts.map((amount) => ({
            text: amount.text,
            start: amount.start,
            end: amount.end,
            value: amount.value,
            usedDecimal: amount.usedDecimal,
            usedGrouping: amount.usedGrouping,
          })),
          ...scan.invalid.map((invalid) => ({
            text: invalid.text,
            start: invalid.start,
            end: invalid.end,
            usedDecimal: false,
            usedGrouping: false,
            issue: invalid.issue,
          })),
        ];
        const structuredTokens = amountTokens.some((token) => token.usedDecimal || token.usedGrouping);

        if (!amountTokens.length) {
          if ((markerIds.length || placeholder) && (placeholder || isPlaceholderText(surface.text))) {
            const candidate = makeCandidate(surface, scope.id, 0, surface.text.length, surface.text, context, ['placeholder']);
            if (requireCurrentMarker && !markerIds.length) candidate.reasons.push('unmarked');
            candidates.push(candidate);
            allCandidates.push(candidate);
          }
          continue;
        }

        if (!hasContext && !structuredTokens && !amountTokens.some((token) => token.issue)) {
          continue;
        }

        for (const token of amountTokens) {
          const extraReasons: string[] = [];
          if (requireCurrentMarker && !markerIds.length) extraReasons.push('unmarked');
          if (amountTokens.length > 1) extraReasons.push('ambiguous-amount');
          if (token.issue) extraReasons.push('invalid-number');

          const currency = currencyForCandidate(
            surface,
            token.start,
            token.end,
            scope,
            scopeCurrency,
            scopeAttributeCurrency,
            expected,
            requireCurrency,
          );
          if (currency.reasons.length) mergeReasons(extraReasons, currency.reasons);

          const candidate = makeCandidate(
            surface,
            scope.id,
            token.start,
            token.end,
            token.text,
            context,
            extraReasons,
            token.issue ? [token.issue] : [],
          );
          if (token.value !== undefined) {
            candidate.value = token.value;
          }
          if (currency.code) {
            candidate.currency = currency.code;
            candidate.currencyCode = currency.code;
            candidate.currencyEvidence = currency.evidence;
            if (currency.trace) candidate.currencyTrace = currency.trace;
          }

          const blocker = candidate.reasons.some((reason) =>
            [
              'hidden',
              'hidden-ancestor',
              'template',
              'strikethrough',
              'original-price',
              'placeholder',
              'attribute-provenance',
              'currency-mismatch',
              'currency-ambiguous',
              'missing-currency',
              'invalid-number',
              'unmarked',
            ].includes(reason),
          );
          candidate.eligible =
            !blocker &&
            token.value !== undefined &&
            isNonNegativeFinite(token.value) &&
            (currency.code !== undefined);
          if (candidate.eligible) candidate.reasons.push('eligible');
          candidates.push(candidate);
          allCandidates.push(candidate);
        }
      }
    }

    const scopeCandidates = includeRejected ? candidates : candidates.filter((candidate) => candidate.eligible);
    const eligible = scopeCandidates.filter((candidate) => candidate.eligible);
    const structurallyValid = scopeCandidates.filter(
      (candidate) =>
        candidate.value !== undefined &&
        !candidate.reasons.some((reason) =>
          [
            'hidden',
            'hidden-ancestor',
            'template',
            'strikethrough',
            'original-price',
            'placeholder',
            'attribute-provenance',
            'invalid-number',
            'unmarked',
          ].includes(reason),
        ),
    );
    const structuralGroups = new Map<string, PriceCandidate[]>();
    for (const candidate of structurallyValid) {
      const key = `${candidate.value}|${candidate.currencyCode ?? '?'}`;
      const group = structuralGroups.get(key) ?? [];
      group.push(candidate);
      structuralGroups.set(key, group);
    }
    const structuralCurrencyAmbiguity = structurallyValid.filter((candidate) =>
      candidate.reasons.includes('currency-ambiguous'),
    );
    const distinctGroups = new Map<string, PriceCandidate[]>();
    for (const candidate of eligible) {
      const key = dedupeIdenticalAmounts
        ? `${candidate.value}|${candidate.currencyCode}`
        : `${candidate.nodeId}|${candidate.start}|${candidate.value}|${candidate.currencyCode}`;
      const group = distinctGroups.get(key) ?? [];
      group.push(candidate);
      distinctGroups.set(key, group);
    }

    let status: ProductExtraction['status'];
    let price: ResolvedPrice | undefined;
    if (structuralGroups.size > 1) {
      status = 'ambiguous';
      allAmbiguous.push(...structurallyValid);
    } else if (!distinctGroups.size) {
      if (structuralCurrencyAmbiguity.length) {
        status = 'ambiguous';
        allAmbiguous.push(...structuralCurrencyAmbiguity);
      } else {
        status = scopeCandidates.length ? 'invalid' : 'missing';
      }
    } else {
      status = 'resolved';
      const evidence = Array.from(distinctGroups.values())[0];
      const first = evidence[0];
      if (first.value === undefined || !first.currencyCode) {
        status = 'invalid';
      } else {
        price = {
          scopeId: scope.id,
          value: first.value,
          amount: first.value,
          currency: first.currencyCode,
          currencyCode: first.currencyCode,
          rawText: first.amountText,
          text: first.amountText,
          nodeId: first.nodeId,
          start: first.start,
          end: first.end,
          span: first.span,
          ...(first.currencyTrace ? { currencyTrace: first.currencyTrace } : {}),
          currencyEvidence: first.currencyEvidence ?? [],
          evidence,
        };
        prices.push(price);
      }
    }

    products.push({
      scopeId: scope.id,
      scopeNodeId: scope.id,
      status,
      ...(price ? { price } : {}),
      candidates: scopeCandidates,
      scopeTrace: {
        nodeId: scope.id,
        path: scope.path,
        tagName: scope.tagName,
      },
    });
  }

  if (options.strict && configIssues.some((issue) => issue.severity === 'error')) {
    throw new PriceConfigError(configIssues);
  }

  return {
    prices,
    products,
    candidates: includeRejected ? allCandidates : allCandidates.filter((candidate) => candidate.eligible),
    ambiguous: allAmbiguous,
    issues,
  };
}

/** Explicit alias used by some extraction pipelines. */
export const extractPriceEvidence = extractPrices;
export const extractPrice = extractPrices;
export const analyzePrices = extractPrices;
export const extractProductPrices = extractPrices;

export function createPriceExtractor(
  rules: PricingRules,
  options: ExtractionOptions = {},
): (snapshot: DomSnapshot | SnapshotNode | SnapshotNode[]) => PriceExtractionResult {
  return (snapshot) => extractPrices(snapshot, rules, options);
}
