import { createHash } from "node:crypto";
import {
  parseVCard,
  serializeVCard,
  unescapeText,
  type VCard,
  type VCardProperty,
} from "./vcard.ts";

export const HIDDEN_NOTE = "NOTE";
const HIDDEN_PREFIX = "X-PRIVATE-";

export type CollationKind = "i;ascii-casemap" | "i;octet" | "i;unicode-casemap";

export interface VisibilityPolicy {
  canSeeHidden: boolean;
}

export interface TextMatch {
  collation: CollationKind;
  negateCondition: boolean;
  substring: string;
}

export interface PropFilter {
  name: string;
  isNotDefined: boolean;
  textMatch: TextMatch | null;
}

export interface AddressBookFilter {
  test: "anyof" | "allof";
  propFilters: PropFilter[];
}

export interface AddressDataRequest {
  allProps: boolean;
  properties: string[];
}

export interface AddressBookContact {
  href: string;
  vcard: string;
}

export interface AddressBookQuery {
  filter: AddressBookFilter | null;
  nresults: number | null;
  addressData: AddressDataRequest | null;
}

export interface MatchedContact {
  href: string;
  visibleVCard: string;
  etag: string;
}

export interface AddressBookQueryResult {
  contacts: MatchedContact[];
  truncated: boolean;
  limitStatus: typeof INSUFFICIENT_STORAGE | null;
  totalVisibleMatches: number;
}

export const INSUFFICIENT_STORAGE = 507 as const;

export function isHiddenProperty(propertyName: string): boolean {
  const normalized = propertyName.toUpperCase();
  return normalized === HIDDEN_NOTE || normalized.startsWith(HIDDEN_PREFIX);
}

export function isPropertyVisible(
  propertyName: string,
  policy: VisibilityPolicy,
): boolean {
  return policy.canSeeHidden || !isHiddenProperty(propertyName);
}

function visibleProperties(
  card: VCard,
  policy: VisibilityPolicy,
): VCardProperty[] {
  return card.properties.filter((property) =>
    isPropertyVisible(property.name, policy),
  );
}

function normalize(value: string, collation: CollationKind): string {
  switch (collation) {
    case "i;octet":
      return value;
    case "i;unicode-casemap":
      return value.toLocaleLowerCase("en");
    case "i;ascii-casemap":
    default:
      return value.toLowerCase();
  }
}

function propertyMatchesText(
  property: VCardProperty,
  textMatch: TextMatch,
): boolean {
  const haystack = normalize(unescapeText(property.value), textMatch.collation);
  const needle = normalize(textMatch.substring, textMatch.collation);
  const matched = haystack.includes(needle);
  return textMatch.negateCondition ? !matched : matched;
}

function evalPropFilter(
  properties: VCardProperty[],
  filter: PropFilter,
): boolean {
  const sameName = properties.filter(
    (property) => property.name === filter.name.toUpperCase(),
  );

  if (filter.isNotDefined) {
    return sameName.length === 0;
  }

  if (!filter.textMatch) {
    return sameName.length > 0;
  }

  // 对“不可见即不存在”的属性做文本匹配时按未命中处理；
  // 否定条件同样不命中，避免用 negate-condition 反向探测隐藏值。
  if (sameName.length === 0) {
    return false;
  }

  return sameName.some((property) => propertyMatchesText(property, filter.textMatch!));
}

export function cardMatchesFilter(
  vcard: string,
  filter: AddressBookFilter | null,
  policy: VisibilityPolicy,
): boolean {
  if (!filter) {
    return true;
  }
  const card = parseVCard(vcard);
  const properties = visibleProperties(card, policy);

  if (filter.test === "allof") {
    return filter.propFilters.every((propFilter) =>
      evalPropFilter(properties, propFilter),
    );
  }
  return filter.propFilters.some((propFilter) =>
    evalPropFilter(properties, propFilter),
  );
}

export function buildVisibleVCard(
  vcard: string,
  policy: VisibilityPolicy,
  request: AddressDataRequest | null,
): string {
  const card = parseVCard(vcard);
  const visible = visibleProperties(card, policy);

  if (!request || request.allProps) {
    return serializeVCard(visible);
  }

  const requested = new Set(
    request.properties.map((name) => name.toUpperCase()),
  );
  // 隐藏属性即使被显式请求也按“未请求”处理：不报错、不输出。
  const selected = visible.filter(
    (property) =>
      property.name === "VERSION" || requested.has(property.name),
  );
  return serializeVCard(selected);
}

export function getEtag(vcard: string, policy: VisibilityPolicy): string {
  const visible = buildVisibleVCard(vcard, policy, null);
  const digest = createHash("sha256").update(visible, "utf8").digest("hex");
  return `"${digest}"`;
}

export function runAddressBookQuery(
  contacts: AddressBookContact[],
  query: AddressBookQuery,
  policy: VisibilityPolicy,
): AddressBookQueryResult {
  const matches: MatchedContact[] = [];

  for (const contact of contacts) {
    if (!cardMatchesFilter(contact.vcard, query.filter, policy)) {
      continue;
    }
    matches.push({
      href: contact.href,
      visibleVCard: buildVisibleVCard(
        contact.vcard,
        policy,
        query.addressData,
      ),
      etag: getEtag(contact.vcard, policy),
    });
  }

  const totalVisibleMatches = matches.length;
  const limit = query.nresults;
  let truncated = false;
  let limitStatus: typeof INSUFFICIENT_STORAGE | null = null;

  if (limit !== null && limit >= 0 && totalVisibleMatches > limit) {
    truncated = true;
    limitStatus = INSUFFICIENT_STORAGE;
  }

  const page = limit === null ? matches : matches.slice(0, limit);

  return {
    contacts: page,
    truncated,
    limitStatus,
    totalVisibleMatches,
  };
}

export interface NResultsPage<T> {
  items: T[];
  truncated: boolean;
  limitStatus: typeof INSUFFICIENT_STORAGE | null;
  totalMatches: number;
}

export function applyNResults<T>(items: T[], nresults: number): NResultsPage<T> {
  const totalMatches = items.length;
  const truncated = totalMatches > nresults;
  return {
    items: truncated ? items.slice(0, nresults) : items,
    truncated,
    limitStatus: truncated ? INSUFFICIENT_STORAGE : null,
    totalMatches,
  };
}
