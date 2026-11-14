import type { XmlAttribute, XmlElement } from "./xml.ts";

export const C14N_1_0 = "http://www.w3.org/TR/2001/REC-xml-c14n-20010315";
export const C14N_1_0_WITH_COMMENTS =
  "http://www.w3.org/TR/2001/REC-xml-c14n-20010315#WithComments";
export const EXCLUSIVE_C14N = "http://www.w3.org/2001/10/xml-exc-c14n#";
export const EXCLUSIVE_C14N_WITH_COMMENTS =
  "http://www.w3.org/2001/10/xml-exc-c14n#WithComments";

export interface CanonicalizationOptions {
  readonly excludeElement?: XmlElement;
  readonly inclusivePrefixes?: readonly string[];
}

interface CanonicalizationMode {
  readonly exclusive: boolean;
  readonly comments: boolean;
}

function modeForAlgorithm(algorithm: string): CanonicalizationMode {
  switch (algorithm) {
    case C14N_1_0:
      return { exclusive: false, comments: false };
    case C14N_1_0_WITH_COMMENTS:
      return { exclusive: false, comments: true };
    case EXCLUSIVE_C14N:
      return { exclusive: true, comments: false };
    case EXCLUSIVE_C14N_WITH_COMMENTS:
      return { exclusive: true, comments: true };
    default:
      throw new Error(`Unsupported canonicalization algorithm: ${algorithm}`);
  }
}

function escapeAttribute(value: string): string {
  let result = "";
  for (const character of value) {
    switch (character) {
      case "&":
        result += "&amp;";
        break;
      case "<":
        result += "&lt;";
        break;
      case '"':
        result += "&quot;";
        break;
      case "\t":
        result += "&#x9;";
        break;
      case "\n":
        result += "&#xA;";
        break;
      case "\r":
        result += "&#xD;";
        break;
      default:
        result += character;
        break;
    }
  }
  return result;
}

function escapeText(value: string): string {
  let result = "";
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (character === "&") {
      result += "&amp;";
    } else if (character === "<") {
      result += "&lt;";
    } else if (character === ">") {
      result += "&gt;";
    } else if (character === "\r") {
      result += "&#xD;";
    } else {
      result += character;
    }
  }
  return result;
}

function compareAttributes(left: XmlAttribute, right: XmlAttribute): number {
  if (left.namespaceUri < right.namespaceUri) return -1;
  if (left.namespaceUri > right.namespaceUri) return 1;
  if (left.localName < right.localName) return -1;
  if (left.localName > right.localName) return 1;
  return 0;
}

function compareNamespaceDeclarations(
  left: readonly [string, string],
  right: readonly [string, string],
): number {
  if (left[0] < right[0]) return -1;
  if (left[0] > right[0]) return 1;
  return 0;
}

function desiredInclusiveNamespaces(
  element: XmlElement,
  context: ReadonlyMap<string, string>,
): Map<string, string> {
  const desired = new Map<string, string>();
  for (const [prefix, namespaceUri] of Object.entries(element.namespacesInScope)) {
    if (prefix === "xml") {
      continue;
    }
    desired.set(prefix, namespaceUri);
  }
  if (element.prefix === null && element.namespaceUri.length === 0) {
    if (context.get("") !== undefined && context.get("") !== "") {
      desired.set("", "");
    }
  } else if (element.prefix === null && element.namespaceUri.length > 0) {
    desired.set("", element.namespaceUri);
  }
  return desired;
}

function desiredExclusiveNamespaces(
  element: XmlElement,
  inclusivePrefixes: readonly string[],
): Map<string, string> {
  const desired = new Map<string, string>();

  if (element.prefix === null) {
    if (element.namespaceUri.length > 0) {
      desired.set("", element.namespaceUri);
    }
  } else if (element.prefix !== "xml") {
    desired.set(element.prefix, element.namespaceUri);
  }

  for (const attribute of element.attributes) {
    if (attribute.prefix !== null && attribute.prefix !== "xml") {
      desired.set(attribute.prefix, attribute.namespaceUri);
    }
  }

  for (const prefix of inclusivePrefixes) {
    const actualPrefix = prefix === "#default" ? "" : prefix;
    if (actualPrefix === "xml") {
      continue;
    }
    const namespaceUri = element.namespacesInScope[actualPrefix];
    if (namespaceUri !== undefined) {
      desired.set(actualPrefix, namespaceUri);
    }
  }

  return desired;
}

function declarationsToSerialize(
  element: XmlElement,
  context: ReadonlyMap<string, string>,
  mode: CanonicalizationMode,
  inclusivePrefixes: readonly string[],
): Array<readonly [string, string]> {
  const desired = mode.exclusive
    ? desiredExclusiveNamespaces(element, inclusivePrefixes)
    : desiredInclusiveNamespaces(element, context);
  const declarations: Array<readonly [string, string]> = [];

  for (const [prefix, namespaceUri] of desired) {
    const rendered = context.get(prefix);
    if (rendered === undefined) {
      if (namespaceUri === "" && prefix === "" && !mode.exclusive) {
        continue;
      }
      declarations.push([prefix, namespaceUri]);
    } else if (rendered !== namespaceUri) {
      declarations.push([prefix, namespaceUri]);
    }
  }

  if (mode.exclusive && element.prefix === null) {
    const renderedDefault = context.get("");
    if (
      element.namespaceUri.length === 0 &&
      renderedDefault !== undefined &&
      renderedDefault !== "" &&
      !desired.has("") &&
      !declarations.some(([prefix]) => prefix === "")
    ) {
      declarations.push(["", ""]);
    }
  }

  declarations.sort(compareNamespaceDeclarations);
  return declarations;
}

function renderElement(
  element: XmlElement,
  mode: CanonicalizationMode,
  inclusivePrefixes: readonly string[],
  excludeElement: XmlElement | undefined,
  context: Map<string, string>,
  output: string[],
): void {
  if (excludeElement === element) {
    return;
  }

  const declarations = declarationsToSerialize(element, context, mode, inclusivePrefixes);
  const previousValues = new Map<string, string | undefined>();

  output.push("<", element.qName);
  for (const [prefix, namespaceUri] of declarations) {
    previousValues.set(prefix, context.get(prefix));
    context.set(prefix, namespaceUri);
    if (prefix === "") {
      output.push(' xmlns="', escapeAttribute(namespaceUri), '"');
    } else {
      output.push(" xmlns:", prefix, '="', escapeAttribute(namespaceUri), '"');
    }
  }

  const attributes = [...element.attributes].sort(compareAttributes);
  for (const attribute of attributes) {
    output.push(" ", attribute.qName, '="', escapeAttribute(attribute.value), '"');
  }
  output.push(">");

  for (const child of element.children) {
    switch (child.kind) {
      case "element":
        renderElement(child, mode, inclusivePrefixes, excludeElement, context, output);
        break;
      case "text":
        output.push(escapeText(child.value));
        break;
      case "comment":
        if (mode.comments) {
          output.push("<!--", child.value, "-->");
        }
        break;
      case "processing-instruction":
        output.push("<?", child.target);
        if (child.value.length > 0) {
          output.push(" ", child.value);
        }
        output.push("?>");
        break;
    }
  }

  output.push("</", element.qName, ">");

  for (const [prefix, previous] of previousValues) {
    if (previous === undefined) {
      context.delete(prefix);
    } else {
      context.set(prefix, previous);
    }
  }
}

/**
 * Canonicalizes an element subtree using C14N 1.0 or exclusive C14N.
 * Document-inherited namespaces are materialized at the subtree apex.
 */
export function canonicalizeXml(
  element: XmlElement,
  algorithm: string,
  options: CanonicalizationOptions = {},
): Buffer {
  const mode = modeForAlgorithm(algorithm);
  if (mode.exclusive && options.inclusivePrefixes !== undefined) {
    for (const prefix of options.inclusivePrefixes) {
      if (prefix !== "#default" && element.namespacesInScope[prefix] === undefined && prefix !== "xml") {
        throw new Error(`InclusiveNamespaces prefix is not bound: ${prefix}`);
      }
    }
  }
  if (!mode.exclusive && options.inclusivePrefixes !== undefined && options.inclusivePrefixes.length > 0) {
    throw new Error("InclusiveNamespaces is only valid with exclusive canonicalization");
  }

  const output: string[] = [];
  renderElement(
    element,
    mode,
    options.inclusivePrefixes ?? [],
    options.excludeElement,
    new Map<string, string>(),
    output,
  );
  return Buffer.from(output.join(""), "utf8");
}

export function isSupportedCanonicalizationAlgorithm(algorithm: string): boolean {
  return (
    algorithm === C14N_1_0 ||
    algorithm === C14N_1_0_WITH_COMMENTS ||
    algorithm === EXCLUSIVE_C14N ||
    algorithm === EXCLUSIVE_C14N_WITH_COMMENTS
  );
}

export function isExclusiveCanonicalizationAlgorithm(algorithm: string): boolean {
  return algorithm === EXCLUSIVE_C14N || algorithm === EXCLUSIVE_C14N_WITH_COMMENTS;
}
