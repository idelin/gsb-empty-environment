import { serialize } from './serializer.ts';
import { TreeBuilder } from './tree-builder.ts';
import { checkUrl, isSafeStyleAttribute } from './url-safety.ts';
import {
  DEFAULT_ALLOWED_PROTOCOLS,
  HARD_STRIPPED_TAGS,
  URI_ATTRIBUTES,
  isAllowedAttribute,
  isAllowedTag,
} from './policy.ts';
import type {
  ChildNode,
  DocumentNode,
  ElementNode,
  SanitizeOptions,
  SanitizeResult,
  SourceLocation,
  Violation,
} from './types.ts';
import { NS_HTML, NS_MATHML, NS_SVG, SanitizationError } from './types.ts';
import { verifyIdempotency } from './idempotency.ts';

/** HTML tags that can never survive while nested in foreign content. */
const FOREIGN_FORBIDDEN_HTML_TAGS = new Set([
  'table', 'form', 'style', 'script', 'iframe', 'object', 'embed',
  'base', 'meta', 'noscript', 'link', 'title', 'template', 'frame',
  'frameset', 'input', 'button', 'textarea', 'select', 'option',
]);

interface AuditContext {
  violations: Violation[];
  path: string[];
}

type ResolvedOptions = {
  rejectReplacementChar: boolean;
  allowStyleAttribute: boolean;
  keepComments: boolean;
  allowedProtocols: Set<string>;
};

/**
 * Whitelist sanitizer with explicit foreign-content structural auditing and
 * mandatory parse/serialize/re-parse idempotency verification.
 */
export function sanitize(
  input: string,
  options: SanitizeOptions = {},
): SanitizeResult {
  const resolved: ResolvedOptions = {
    rejectReplacementChar: options.rejectReplacementChar ?? true,
    allowStyleAttribute: options.allowStyleAttribute ?? false,
    keepComments: options.keepComments ?? false,
    allowedProtocols: new Set([
      ...DEFAULT_ALLOWED_PROTOCOLS,
      ...(options.allowedProtocols ?? []),
    ]),
  };

  const builder = new TreeBuilder(input);
  const tree = builder.parse();
  const context: AuditContext = { violations: [], path: [] };

  for (const event of builder.structuralEvents) {
    if (event.type === 'foster-parent' &&
      (event.tagName === 'table' || event.tagName === 'form')) {
      context.violations.push({
        kind: 'dangerous-nesting',
        message:
          `<${event.tagName}> was foster-parented, indicating a ` +
          'foreign-content/table nesting that browsers re-structure',
        location: event.location,
        path: '#document',
      });
    }
    if (event.type === 'namespace-breakout' &&
      (event.tagName === 'table' || event.tagName === 'form' ||
        event.tagName === 'style' || event.tagName === 'script')) {
      context.violations.push({
        kind: 'dangerous-nesting',
        message:
          `<${event.tagName}> triggered a foreign-content namespace ` +
          `break-out (${event.reason}); re-parse mode may differ`,
        location: event.location,
        path: '#document',
      });
    }
  }

  const cleaned = cleanDocument(tree, context, resolved);
  const html = serialize(cleaned);

  const idempotency = verifyIdempotency(cleaned, html);
  if (!idempotency.stable) {
    context.violations.push(...idempotency.violations);
  }

  const result: SanitizeResult = {
    html,
    stable: idempotency.stable,
    violations: context.violations,
    tree: cleaned,
  };

  const hardFailures = context.violations.filter(
    (violation) =>
      violation.kind === 'dangerous-nesting' ||
      violation.kind === 'dangerous-comment' ||
      violation.kind === 'replacement-character' ||
      violation.kind === 'idempotency-drift',
  );
  if (hardFailures.length > 0) {
    throw new SanitizationError(hardFailures);
  }
  return result;
}

function cleanDocument(
  document: DocumentNode,
  context: AuditContext,
  options: ResolvedOptions,
): DocumentNode {
  return {
    ...document,
    children: cleanChildren(document.children, context, options),
  };
}

function cleanChildren(
  children: ChildNode[],
  context: AuditContext,
  options: ResolvedOptions,
): ChildNode[] {
  const output: ChildNode[] = [];
  for (const child of children) {
    if (child.type === 'text') {
      auditReplacementChar(
        child.data, child.location, context, options, 'text node',
      );
      output.push(child);
      continue;
    }
    if (child.type === 'comment') {
      auditComment(child.data, child.location, context);
      if (options.keepComments) {
        output.push(child);
      }
      continue;
    }
    const result = cleanElement(child, context, options);
    if (!result.removeSubtree && result.nodes) {
      output.push(...result.nodes);
    }
  }
  return output;
}

interface ElementCleanResult {
  removeSubtree: boolean;
  nodes?: ChildNode[];
}

function cleanElement(
  element: ElementNode,
  context: AuditContext,
  options: ResolvedOptions,
): ElementCleanResult {
  const label = `${namespaceLabel(element.namespace)}:${element.tagName}`;
  context.path.push(label);

  if (HARD_STRIPPED_TAGS.has(element.tagName)) {
    const insideForeign = context.path.length > 1 &&
      context.path.slice(0, -1).some(
        (entry) => entry.startsWith('svg:') || entry.startsWith('mathml:'),
      );
    if (insideForeign) {
      context.violations.push({
        kind: 'dangerous-nesting',
        message:
          `Banned element <${element.tagName}> was nested in foreign ` +
          'content and removed; this pattern enables namespace mXSS',
        location: element.location,
        path: currentPath(context),
        excerpt: `<${element.tagName}>`,
      });
    }
    context.violations.push({
      kind: 'banned-element',
      message: `Banned element <${element.tagName}> removed with subtree`,
      location: element.location,
      path: currentPath(context),
      excerpt: `<${element.tagName}>`,
    });
    context.path.pop();
    return { removeSubtree: true };
  }

  auditDangerousNesting(element, context);

  const attributes = cleanAttributes(element, context, options);
  const children = cleanChildren(element.children, context, options);
  const cleaned: ElementNode = { ...element, attributes, children };

  if (!isAllowedTag(element.tagName, element.namespace)) {
    if (element.namespace !== NS_HTML) {
      context.violations.push({
        kind: 'dangerous-nesting',
        message:
          `Non-whitelisted element <${element.tagName}> in foreign ` +
          `${namespaceLabel(element.namespace)} namespace rejected`,
        location: element.location,
        path: currentPath(context),
      });
      context.path.pop();
      return { removeSubtree: true };
    }
    context.violations.push({
      kind: 'banned-element',
      message: `Non-whitelisted element <${element.tagName}> unwrapped`,
      location: element.location,
      path: currentPath(context),
    });
    context.path.pop();
    return { removeSubtree: false, nodes: children };
  }

  context.path.pop();
  return { removeSubtree: false, nodes: [cleaned] };
}

/**
 * Structural guard: an HTML element whose tag can alter the browser's
 * insertion or namespace modes must never survive inside foreign content
 * unless it sits at a genuine HTML integration point (the tree builder has
 * already materialized such transitions with HTML-namespace ancestry).
 */
function auditDangerousNesting(
  element: ElementNode,
  context: AuditContext,
): void {
  const ancestorEntries = context.path.slice(0, -1);
  const foreignAncestors = ancestorEntries.filter(
    (entry) => entry.startsWith('svg:') || entry.startsWith('mathml:'),
  );
  if (foreignAncestors.length === 0) {
    return;
  }
  if (element.namespace !== NS_HTML) {
    // A foreign-namespace table/style still participates in the browser's
    // foreign-content mode transitions and must be treated as structural.
    if (element.tagName === 'table' || element.tagName === 'form' ||
      element.tagName === 'style' || element.tagName === 'script') {
      context.violations.push({
        kind: 'dangerous-nesting',
        message:
          `Foreign-content <${element.tagName}> nesting can trigger a ` +
          'namespace mode jump during re-parse',
        location: element.location,
        path: currentPath(context),
      });
    }
    return;
  }
  if (FOREIGN_FORBIDDEN_HTML_TAGS.has(element.tagName)) {
    context.violations.push({
      kind: 'dangerous-nesting',
      message:
        `Forbidden HTML element <${element.tagName}> nested in foreign ` +
        'content; a browser re-parse could restructure the tree',
      location: element.location,
      path: currentPath(context),
    });
  }
}

function cleanAttributes(
  element: ElementNode,
  context: AuditContext,
  options: ResolvedOptions,
): ElementNode['attributes'] {
  const output: ElementNode['attributes'] = [];
  for (const attribute of element.attributes) {
    const name = attribute.name;
    if (name.startsWith('on')) {
      context.violations.push({
        kind: 'banned-attribute',
        message: `Event handler attribute "${name}" removed`,
        location: attribute.location,
        path: currentPath(context),
        excerpt: `${name}=${attribute.value.slice(0, 40)}`,
      });
      continue;
    }
    if (!isAllowedAttribute(element.tagName, element.namespace, name)) {
      context.violations.push({
        kind: 'banned-attribute',
        message: `Attribute "${name}" not in allow-list, removed`,
        location: attribute.location,
        path: currentPath(context),
      });
      continue;
    }
    if (name === 'style') {
      if (!options.allowStyleAttribute) {
        context.violations.push({
          kind: 'banned-attribute',
          message: 'style attribute disabled by default',
          location: attribute.location,
          path: currentPath(context),
        });
        continue;
      }
      if (!isSafeStyleAttribute(attribute.value)) {
        context.violations.push({
          kind: 'banned-attribute',
          message: 'Unsafe style attribute removed',
          location: attribute.location,
          path: currentPath(context),
        });
        continue;
      }
    }
    if (URI_ATTRIBUTES.has(name)) {
      const result = checkUrl(attribute.value, options.allowedProtocols);
      if (!result.safe) {
        context.violations.push({
          kind: 'banned-protocol',
          message:
            `URI attribute "${name}" uses a disallowed or obfuscated ` +
            `protocol (normalized: "${result.normalized.slice(0, 60)}")`,
          location: attribute.location,
          path: currentPath(context),
          excerpt: attribute.value.slice(0, 60),
        });
        continue;
      }
    }
    if (name === 'srcset') {
      const candidates = attribute.value.split(',');
      const unsafe = candidates.some((candidate) => {
        const url = candidate.trim().split(/\s+/)[0] ?? '';
        return !checkUrl(url, options.allowedProtocols).safe;
      });
      if (unsafe) {
        context.violations.push({
          kind: 'banned-protocol',
          message: 'srcset contains a disallowed or obfuscated URL',
          location: attribute.location,
          path: currentPath(context),
          excerpt: attribute.value.slice(0, 60),
        });
        continue;
      }
    }
    auditReplacementChar(
      attribute.value,
      attribute.location,
      context,
      options,
      `attribute "${name}"`,
    );
    if (options.rejectReplacementChar && attribute.value.includes('�')) {
      continue;
    }
    output.push(attribute);
  }
  return output;
}

function auditComment(
  data: string,
  location: SourceLocation,
  context: AuditContext,
): void {
  const dangerous = data.includes('</') ||
    data.includes('-->') ||
    data.includes('<!--') ||
    /<\s*\/?\s*[a-zA-Z]/.test(data) ||
    data.startsWith('>') ||
    data.startsWith('->');
  if (!dangerous) {
    return;
  }
  context.violations.push({
    kind: 'dangerous-comment',
    message:
      'Comment contains closing-tag markup or comment boundary spoofing',
    location,
    path: currentPath(context),
    excerpt: data.slice(0, 60),
  });
}

function auditReplacementChar(
  data: string,
  location: SourceLocation,
  context: AuditContext,
  options: ResolvedOptions,
  what: string,
): void {
  if (!options.rejectReplacementChar || !data.includes('�')) {
    return;
  }
  context.violations.push({
    kind: 'replacement-character',
    message: `U+FFFD replacement character found in ${what}`,
    location,
    path: currentPath(context),
    excerpt: data.slice(0, 60),
  });
}

function currentPath(context: AuditContext): string {
  return context.path.length > 0 ? context.path.join(' > ') : '#document';
}

function namespaceLabel(
  namespace: ElementNode['namespace'],
): string {
  if (namespace === NS_SVG) {
    return 'svg';
  }
  if (namespace === NS_MATHML) {
    return 'mathml';
  }
  return 'html';
}
