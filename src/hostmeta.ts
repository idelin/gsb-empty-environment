import type { HostMetaLink } from "./types.ts";
import {
  encodeAcctForQuery,
  hostOfUrl,
  normalizeHost,
  tryToHttpsUrl,
} from "./uri.ts";

const LINK_ELEMENT = /<(?:[A-Za-z_][\w.-]*:)?Link\b([^>]*?)(?:\/?)>/gi;
const ATTRIBUTE = /([A-Za-z_][\w:.-]*)\s*=\s*("([^"]*)"|'([^']*)')/g;

function decodeXmlEntities(value: string): string {
  return value.replace(
    /&(?:#x([0-9A-Fa-f]+)|#([0-9]+)|(amp|lt|gt|quot|apos));/g,
    (_match, hex: string | undefined, decimal: string | undefined, named: string | undefined) => {
      if (hex || decimal) {
        const codePoint = Number.parseInt(hex ?? decimal ?? "", hex ? 16 : 10);
        if (codePoint >= 0 && codePoint <= 0x10ffff) {
          return String.fromCodePoint(codePoint);
        }
        return _match;
      }
      switch (named) {
        case "amp":
          return "&";
        case "lt":
          return "<";
        case "gt":
          return ">";
        case "quot":
          return '"';
        case "apos":
          return "'";
        default:
          return _match;
      }
    },
  );
}

function localName(name: string): string {
  const colon = name.lastIndexOf(":");
  return (colon === -1 ? name : name.slice(colon + 1)).toLowerCase();
}

/**
 * Extract Link elements from an XRD host-meta document without pulling in an
 * XML parser. Only Link elements and their attributes participate in the
 * security decision, so malformed unrelated XML is irrelevant.
 */
export function parseHostMetaLinks(xml: string): HostMetaLink[] {
  if (typeof xml !== "string" || xml.length === 0) {
    return [];
  }

  const withoutComments = xml.replace(/<!--[\s\S]*?-->/g, "");
  const links: HostMetaLink[] = [];

  for (const match of withoutComments.matchAll(LINK_ELEMENT)) {
    const attributes: Record<string, string> = Object.create(null);
    const attributeText = match[1] ?? "";
    for (const attributeMatch of attributeText.matchAll(ATTRIBUTE)) {
      const name = localName(attributeMatch[1]);
      const value = attributeMatch[3] ?? attributeMatch[4] ?? "";
      attributes[name] = decodeXmlEntities(value);
    }

    if (attributes.rel) {
      links.push(attributes as unknown as HostMetaLink);
    }
  }

  return links;
}

function hasLrddRelation(rel: string): boolean {
  return rel
    .split(/\s+/)
    .some((token) => token.toLowerCase() === "lrdd");
}

/**
 * Validate an lrdd URI template against the host being resolved.
 *
 * The template must be an absolute HTTPS URL containing the literal `{uri}`
 * variable, and its authority must equal the queried host. The variable is
 * replaced with a harmless probe before parsing so that a host-position
 * template cannot slip an attacker-controlled authority past validation.
 */
export function validateLrddTemplate(
  template: string,
  queryHost: string,
): string | null {
  if (typeof template !== "string" || !template.includes("{uri}")) {
    return null;
  }

  let normalizedQueryHost: string;
  try {
    normalizedQueryHost = normalizeHost(queryHost);
  } catch {
    return null;
  }

  const probe = template.replaceAll("{uri}", "acct%3Aprobe%40example.invalid");
  const parsed = tryToHttpsUrl(probe);
  if (!parsed || hostOfUrl(parsed) !== normalizedQueryHost) {
    return null;
  }

  return template;
}

export function selectLrddTemplate(
  xml: string,
  queryHost: string,
): string | null {
  for (const link of parseHostMetaLinks(xml)) {
    if (
      link.rel &&
      hasLrddRelation(link.rel) &&
      typeof link.template === "string"
    ) {
      const template = validateLrddTemplate(link.template, queryHost);
      if (template) {
        return template;
      }
    }
  }
  return null;
}

export function expandLrddTemplate(
  template: string,
  acctUri: string,
  queryHost: string,
): string | null {
  const validated = validateLrddTemplate(template, queryHost);
  if (!validated) {
    return null;
  }

  const expanded = validated.replaceAll("{uri}", encodeAcctForQuery(acctUri));
  const decodedAsUrl = tryToHttpsUrl(expanded);
  if (!decodedAsUrl || hostOfUrl(decodedAsUrl) !== normalizeHost(queryHost)) {
    return null;
  }
  return expanded;
}
