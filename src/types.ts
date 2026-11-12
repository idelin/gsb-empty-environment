/**
 * Public data contract for the price-evidence parser.
 *
 * The parser deliberately works on a serialized DOM snapshot rather than a live
 * DOM.  A snapshot can be produced by a browser (or by a fixture) and must carry
 * enough information to answer three questions: what was visible, what styles
 * applied, and where did an attribute value come from.
 */

export type SnapshotScalar = string | number | boolean | null | undefined;

export interface AttributeSnapshot {
  value?: SnapshotScalar;
  text?: SnapshotScalar;
  content?: SnapshotScalar;
  /** "template", "rendered", "computed", "default", ... */
  source?: string | { kind?: string; type?: string };
  provenance?: string | { kind?: string; type?: string; fromTemplate?: boolean };
  origin?: string;
  fromTemplate?: boolean;
  [key: string]: unknown;
}

export type SnapshotAttribute = SnapshotScalar | AttributeSnapshot;

export interface SnapshotAttributeEntry {
  name: string;
  value?: SnapshotAttribute;
  text?: string;
  content?: string;
  source?: string | { kind?: string; type?: string };
  provenance?: string | { kind?: string; type?: string };
  origin?: string;
  fromTemplate?: boolean;
}

export type SnapshotAttributeBag =
  | Record<string, SnapshotAttribute>
  | SnapshotAttributeEntry[];

export interface VisibilitySnapshot {
  visible?: boolean;
  hidden?: boolean;
  value?: boolean | string;
  state?: string;
  reason?: string;
  hiddenByAncestor?: boolean;
  ancestorId?: string;
  [key: string]: unknown;
}

export interface SnapshotNode {
  /** Stable snapshot identity.  It is echoed in every trace. */
  id?: string;
  nodeId?: string;
  type?: string;
  kind?: string;
  nodeType?: number;
  nodeValue?: string;
  tagName?: string;
  tag?: string;
  localName?: string;
  name?: string;
  className?: string;
  classList?: string[];
  attributes?: SnapshotAttributeBag;
  attrs?: SnapshotAttributeBag;
  attribs?: SnapshotAttributeBag;
  /** Per-attribute provenance when values themselves are plain strings. */
  attributeSources?: Record<string, string | { kind?: string; source?: string }>;
  /** Own text when this is a text node, or the text of a childless element. */
  text?: string;
  value?: string | number;
  textContent?: string;
  data?: string;
  children?: SnapshotNode[];
  childNodes?: SnapshotNode[];
  nodes?: SnapshotNode[];
  /** Contents of a <template> node. */
  content?: SnapshotNode | SnapshotNode[];
  templateContent?: SnapshotNode | SnapshotNode[];
  style?: string | Record<string, SnapshotScalar>;
  styles?: Record<string, SnapshotScalar>;
  computedStyle?: string | Record<string, SnapshotScalar>;
  css?: Record<string, SnapshotScalar>;
  visible?: boolean;
  hidden?: boolean;
  isVisible?: boolean;
  rendered?: boolean;
  display?: string;
  opacity?: number | string;
  textDecoration?: string;
  textDecorationLine?: string;
  hiddenByAncestor?: boolean;
  visibility?: boolean | string | VisibilitySnapshot;
  inTemplate?: boolean;
  isTemplate?: boolean;
  fromTemplate?: boolean;
  template?: boolean;
  provenance?: string | { kind?: string; source?: string; origin?: string; type?: string; fromTemplate?: boolean };
  origin?: string;
  source?: string;
  parentId?: string;
  parent?: SnapshotNode;
  path?: string | number[];
  [key: string]: unknown;
}

export interface DomSnapshot extends SnapshotNode {
  /** Preferred entry point.  When present it takes precedence over the wrapper itself. */
  root?: SnapshotNode;
  document?: SnapshotNode;
  html?: SnapshotNode;
}

export interface NormalizedAttribute {
  name: string;
  value: string;
  raw: SnapshotAttribute;
  provenance: string;
  provenanceDetail?: string;
  fromTemplate: boolean;
}

export interface NormalizedNode {
  id: string;
  /** The id supplied by the snapshot, before duplicate disambiguation. */
  rawId?: string;
  kind: 'document' | 'element' | 'text' | 'other';
  tagName: string;
  attributes: NormalizedAttribute[];
  attributeMap: Record<string, NormalizedAttribute>;
  text: string | null;
  children: NormalizedNode[];
  parent: NormalizedNode | null;
  path: number[];
  depth: number;
  ownHidden: boolean;
  ownHiddenReason: string | null;
  hiddenByAncestor: boolean;
  inTemplate: boolean;
  provenance: string;
  style: Record<string, string>;
  raw: SnapshotNode;
}

/** A matcher is intentionally permissive: fixtures and site configs can use
 * selectors, simple structural predicates, or explicit predicates. */
export type MatcherFunction = (node: NormalizedNode) => boolean;

export interface MatcherObject {
  selector?: string | string[];
  tagName?: string | string[];
  tag?: string | string[];
  id?: string | string[];
  class?: string | string[];
  className?: string | string[];
  attribute?: string | string[] | Record<string, string | number | boolean>;
  attributes?: string | string[] | Record<string, string | number | boolean>;
  predicate?: MatcherFunction;
  any?: NodeMatcher[];
  all?: NodeMatcher[];
  not?: NodeMatcher;
}

export type NodeMatcher = string | string[] | MatcherFunction | MatcherObject;

export interface NumberFormatRule {
  decimal?: string;
  decimalSeparator?: string;
  decimalPoint?: string;
  decimalChar?: string;
  radix?: string;
  grouping?: string | string[] | null;
  groupingSeparator?: string | string[] | null;
  groupSeparator?: string | string[] | null;
  groupChar?: string;
  thousands?: string | string[] | null;
  thousandsSeparator?: string | string[] | null;
  groupingSize?: number;
  groupingSizes?: number[];
  maxFractionDigits?: number;
  allowNegative?: boolean;
  allowLeadingPlus?: boolean;
}

export interface CurrencyRule {
  code?: string;
  currency?: string;
  expected?: string;
  symbol?: string;
  symbols?: string[];
  codes?: string[];
  caseSensitive?: boolean;
  allowSymbolAmbiguity?: boolean;
}

export type CurrencyInput = string | CurrencyRule | string[] | CurrencyRule[];

export interface PricingRules {
  /** Element that owns one product's fields. */
  productScope?: NodeMatcher;
  scope?: NodeMatcher;
  scopeSelector?: string | string[];
  productScopeSelector?: string | string[];
  productCard?: NodeMatcher;
  productSelector?: string | string[];
  /** Marker for the amount that may be used as the current price. */
  currentPriceMarker?: NodeMatcher;
  currentPrice?: NodeMatcher;
  priceMarker?: NodeMatcher;
  currentPriceSelector?: string | string[];
  priceSelector?: string | string[];
  currentPriceAttribute?: string | string[];
  currentPriceAttributes?: string | string[];
  /** Optional marker for a struck-through / compare-at price. */
  originalPriceMarker?: NodeMatcher;
  originalPrice?: NodeMatcher;
  oldPriceMarker?: NodeMatcher;
  originalPriceSelector?: string | string[];
  /** Attribute names that may contain an amount. */
  priceAttributes?: string | string[];
  amountAttributes?: string | string[];
  priceAttribute?: string | string[];
  amountAttribute?: string | string[];
  numberFormat?: NumberFormatRule;
  format?: NumberFormatRule;
  decimal?: string;
  decimalSeparator?: string;
  decimalChar?: string;
  grouping?: string | string[] | null;
  groupingSeparator?: string | string[] | null;
  groupChar?: string;
  thousandSeparator?: string | string[] | null;
  groupingSize?: number;
  groupingSizes?: number[];
  currency?: CurrencyInput;
  currencyCode?: string;
  currencySymbol?: string;
  expectedCurrency?: string;
  currencies?: string[];
  requireCurrentMarker?: boolean;
  requireCurrency?: boolean;
  /** Keep rejected evidence in the result (default true). */
  includeRejectedCandidates?: boolean;
}

export interface ExtractionOptions {
  /** Throw a PriceConfigError instead of returning config issues. */
  strict?: boolean;
  /** Keep rejected (hidden/struck/template/old) evidence. Defaults to true. */
  includeRejectedCandidates?: boolean;
  /** Merge candidates that agree on value and currency. Defaults to true. */
  dedupeIdenticalAmounts?: boolean;
}

export type TraceSource = 'text' | 'attribute';

export interface TraceSpan {
  nodeId: string;
  /** UTF-16 code-unit offsets into the node's text or attribute value. */
  start: number;
  end: number;
  text: string;
  source: TraceSource;
  attribute?: string;
}

export interface AmountIssue {
  code: string;
  message: string;
  start: number;
  end: number;
  text: string;
}

export interface ParsedAmount {
  text: string;
  start: number;
  end: number;
  value: number;
  integerDigits: string;
  fractionDigits: string;
  usedGrouping: boolean;
  usedDecimal: boolean;
}

export interface InvalidAmountToken {
  text: string;
  start: number;
  end: number;
  issue: AmountIssue;
}

export interface AmountScanResult {
  amounts: ParsedAmount[];
  invalid: InvalidAmountToken[];
}

export interface CurrencyMention {
  start: number;
  end: number;
  text: string;
  /** All ISO codes the token could denote; config or context can disambiguate. */
  codes: string[];
  kind: 'symbol' | 'code';
}

export interface CurrencyEvidence {
  code: string;
  source: 'text' | 'attribute' | 'config';
  trace?: TraceSpan;
  raw?: string;
  ambiguousCodes?: string[];
}

export type CandidateReason =
  | 'eligible'
  | 'hidden'
  | 'hidden-ancestor'
  | 'template'
  | 'strikethrough'
  | 'original-price'
  | 'unmarked'
  | 'placeholder'
  | 'attribute-provenance'
  | 'currency-mismatch'
  | 'currency-ambiguous'
  | 'missing-currency'
  | 'invalid-number'
  | 'ambiguous-amount'
  | 'outside-product-scope'
  | string;

export interface PriceCandidate {
  scopeId: string | null;
  nodeId: string;
  start: number;
  end: number;
  text: string;
  amountText: string;
  source: TraceSource;
  attribute?: string;
  fullText: string;
  value?: number;
  currency?: string;
  currencyCode?: string;
  currencyTrace?: TraceSpan;
  currencyEvidence?: CurrencyEvidence[];
  eligible: boolean;
  reasons: CandidateReason[];
  markerIds: string[];
  originalMarkerIds: string[];
  fieldNodeId?: string;
  issues: AmountIssue[];
  span: TraceSpan;
}

export interface ExtractionIssue {
  code: string;
  severity: 'error' | 'warning' | 'info';
  message: string;
  scopeId?: string;
  nodeId?: string;
  span?: TraceSpan;
}

export interface ResolvedPrice {
  scopeId: string;
  value: number;
  /** Alias of value for extraction pipelines that use amount terminology. */
  amount: number;
  currency: string;
  currencyCode: string;
  rawText: string;
  text: string;
  nodeId: string;
  start: number;
  end: number;
  span: TraceSpan;
  currencyTrace?: TraceSpan;
  currencyEvidence: CurrencyEvidence[];
  evidence: PriceCandidate[];
}

export type ProductStatus = 'resolved' | 'ambiguous' | 'missing' | 'invalid';

export interface ProductExtraction {
  scopeId: string;
  scopeNodeId: string;
  status: ProductStatus;
  price?: ResolvedPrice;
  candidates: PriceCandidate[];
  scopeTrace: {
    nodeId: string;
    path: number[];
    tagName: string;
  };
}

export interface PriceExtractionResult {
  prices: ResolvedPrice[];
  products: ProductExtraction[];
  candidates: PriceCandidate[];
  /** Candidates that prevented a product from resolving. */
  ambiguous: PriceCandidate[];
  issues: ExtractionIssue[];
}

export interface PriceTextParseResult {
  amounts: ParsedAmount[];
  invalid: InvalidAmountToken[];
  ambiguous: boolean;
}
