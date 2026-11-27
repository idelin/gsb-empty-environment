import { TreeBuilder } from './tree-builder.ts';
import { serialize } from './serializer.ts';
import type {
  AnyNode,
  DocumentNode,
  ElementNode,
  SourceLocation,
  Violation,
} from './types.ts';
import { NS_HTML } from './types.ts';

const HTML_VOID_ELEMENTS = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link',
  'meta', 'param', 'source', 'track', 'wbr',
]);

export interface IdempotencyResult {
  stable: boolean;
  violations: Violation[];
}

interface Difference {
  path: string;
  message: string;
  location: SourceLocation;
}

/**
 * Enforce the sanitize -> serialize -> re-parse closed loop. The sanitized
 * AST is serialized, re-parsed with the same HTML5 tree builder, and the
 * re-parsed tree is compared node-for-node against the sanitized AST. Any
 * tag, namespace or attribute drift is reported as an mXSS indicator with
 * the offending payload's source location. A second serialization pass
 * additionally proves textual fixed-point stability.
 */
export function verifyIdempotency(
  sanitizedTree: DocumentNode,
  serialized: string,
): IdempotencyResult {
  const reparsed = new TreeBuilder(serialized).parse();
  const differences: Difference[] = [];

  compareNodes(sanitizedTree, reparsed, '#document', differences);

  const violations: Violation[] = differences.map((difference) => ({
    kind: 'idempotency-drift',
    message:
      `mXSS drift on re-parse: ${difference.message} at ${difference.path}`,
    location: difference.location,
    path: difference.path,
  }));

  if (differences.length > 0) {
    return { stable: false, violations };
  }

  const secondPass = serialize(reparsed);
  if (secondPass !== serialized) {
    violations.push({
      kind: 'idempotency-drift',
      message:
        'Serialized output is not a fixed point across two parse cycles',
      location: sanitizedTree.location,
      path: '#document',
      excerpt: serialized.slice(0, 60),
    });
    return { stable: false, violations };
  }

  return { stable: true, violations: [] };
}

function compareNodes(
  expected: AnyNode,
  actual: AnyNode,
  path: string,
  differences: Difference[],
): void {
  if (expected.type !== actual.type) {
    differences.push({
      path,
      message: `node type changed from ${expected.type} to ${actual.type}`,
      location: actual.location,
    });
    return;
  }

  if (expected.type === 'text' && actual.type === 'text') {
    if (expected.data !== actual.data) {
      differences.push({
        path,
        message: `text content drifted ("${expected.data}" vs "${actual.data}")`,
        location: actual.location,
      });
    }
    return;
  }

  if (expected.type === 'comment' && actual.type === 'comment') {
    if (expected.data !== actual.data) {
      differences.push({
        path,
        message: 'comment data drifted',
        location: actual.location,
      });
    }
    return;
  }

  if (expected.type === 'element' && actual.type === 'element') {
    compareElements(expected, actual, path, differences);
    return;
  }

  if (expected.type === 'document' && actual.type === 'document') {
    compareChildLists(
      expected.children,
      actual.children,
      path,
      differences,
    );
  }
}

function compareElements(
  expected: ElementNode,
  actual: ElementNode,
  path: string,
  differences: Difference[],
): void {
  const elementPath =
    `${path} > ${expected.namespace.split('/').slice(-1)[0]}:${expected.tagName}`;

  if (expected.tagName !== actual.tagName) {
    differences.push({
      path: elementPath,
      message:
        `tag name drifted from <${expected.tagName}> to <${actual.tagName}>`,
      location: actual.location,
    });
  }
  if (expected.namespace !== actual.namespace) {
    differences.push({
      path: elementPath,
      message:
        `namespace drifted from ${expected.namespace} to ${actual.namespace}`,
      location: actual.location,
    });
  }
  compareAttributes(expected, actual, elementPath, differences);
  if (expected.namespace === NS_HTML &&
    HTML_VOID_ELEMENTS.has(expected.tagName)) {
    // Void elements serialize without a closing tag and therefore have no
    // children after a re-parse; their expected child list must be empty.
    if (expected.children.length !== 0 || actual.children.length !== 0) {
      differences.push({
        path: elementPath,
        message: 'void element acquired children',
        location: actual.location,
      });
    }
    return;
  }
  compareChildLists(
    expected.children,
    actual.children,
    elementPath,
    differences,
  );
}

function compareAttributes(
  expected: ElementNode,
  actual: ElementNode,
  path: string,
  differences: Difference[],
): void {
  const expectedMap = new Map(
    expected.attributes.map((attribute) => [attribute.name, attribute.value]),
  );
  const actualMap = new Map(
    actual.attributes.map((attribute) => [attribute.name, attribute.value]),
  );

  for (const [name, value] of expectedMap) {
    if (!actualMap.has(name)) {
      differences.push({
        path,
        message: `attribute "${name}" disappeared on re-parse`,
        location: actual.location,
      });
      continue;
    }
    if (actualMap.get(name) !== value) {
      differences.push({
        path,
        message:
          `attribute "${name}" value drifted ("${value}" vs "${actualMap.get(name)}")`,
        location: actual.location,
      });
    }
  }
  for (const name of actualMap.keys()) {
    if (!expectedMap.has(name)) {
      differences.push({
        path,
        message: `attribute "${name}" appeared on re-parse`,
        location: actual.location,
      });
    }
  }
}

function compareChildLists(
  expected: AnyNode[],
  actual: AnyNode[],
  path: string,
  differences: Difference[],
): void {
  if (expected.length !== actual.length) {
    differences.push({
      path,
      message:
        `child count drifted (${expected.length} vs ${actual.length})`,
      location: actual.length > 0
        ? actual[actual.length - 1].location
        : { offset: 0, line: 1, column: 1 },
    });
    return;
  }
  for (let i = 0; i < expected.length; i += 1) {
    compareNodes(expected[i], actual[i], `${path}[${i}]`, differences);
  }
}
