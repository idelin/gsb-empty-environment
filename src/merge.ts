/**
 * Layered merge engine.
 *
 * Layers are applied in ascending precedence (defaults < file < env < cli).
 * Objects merge key-by-key; scalars and plain arrays replace. Arrays also
 * accept a directive object in a higher-precedence layer:
 *
 *   { "$replace": [...] }                       -- discard lower list, use this one
 *   { "$append": [...] }                        -- keep lower list, append items
 *   { "$remove": [...] }                        -- drop items deep-equal to the listed values
 *   { "$append": [...], "$remove": [...] }      -- directives compose
 */
import type { MergeIssue, SourceKind, SourceRef } from './types.ts';
import { deepEqual, makeNode, type ProvEntry, type ProvNode } from './provenance.ts';
import type { JsonLocation } from './json.ts';

export interface Layer {
  kind: SourceKind;
  name: string;
  value: unknown;
  /** Dotted path -> location inside the layer's source text (files only). */
  locations?: Map<string, JsonLocation>;
  issues?: MergeIssue[];
}

export interface MergeResult {
  value: unknown;
  provenance: ProvNode;
  issues: MergeIssue[];
}

const DIRECTIVE_KEYS = new Set(['$append', '$replace', '$remove']);

interface ListDirective {
  $append?: unknown[];
  $replace?: unknown[];
  $remove?: unknown[];
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function asDirective(v: unknown): ListDirective | undefined {
  if (!isPlainObject(v)) return undefined;
  const keys = Object.keys(v);
  if (keys.length === 0 || !keys.every((k) => DIRECTIVE_KEYS.has(k))) return undefined;
  return v as ListDirective;
}

function sourceAt(layer: Layer, path: string): SourceRef {
  const loc = layer.locations?.get(path);
  return {
    kind: layer.kind,
    name: layer.name,
    location: loc ? `${layer.name}:${loc.line}:${loc.column}` : undefined,
  };
}

/** Build a fresh provenance subtree for a value set wholesale by one layer. */
function buildNode(path: string, value: unknown, entry: ProvEntry, layer: Layer): ProvNode {
  const node = makeNode();
  node.entries.push(entry);
  if (isPlainObject(value)) {
    for (const [k, v] of Object.entries(value)) {
      const childPath = path === '' ? k : `${path}.${k}`;
      node.children.set(
        k,
        buildNode(childPath, v, { source: sourceAt(layer, childPath), value: v }, layer),
      );
    }
  } else if (Array.isArray(value)) {
    value.forEach((item, i) => {
      const childPath = `${path}[${i}]`;
      node.children.set(
        String(i),
        buildNode(childPath, item, { source: sourceAt(layer, childPath), value: item }, layer),
      );
    });
  }
  return node;
}

interface Item {
  value: unknown;
  node: ProvNode;
}

function applyDirective(
  path: string,
  baseItems: Item[],
  directive: ListDirective,
  layer: Layer,
  issues: MergeIssue[],
): Item[] {
  let items: Item[];
  if (directive.$replace !== undefined) {
    if (!Array.isArray(directive.$replace)) {
      issues.push({
        severity: 'error',
        code: 'invalid-list-op',
        path,
        message: `$replace at '${path}' must be an array`,
        source: sourceAt(layer, path),
      });
      return baseItems;
    }
    items = directive.$replace.map((value, i) => ({
      value,
      node: buildNode(`${path}[${i}]`, value, { source: sourceAt(layer, path), value }, layer),
    }));
  } else {
    items = [...baseItems];
  }

  if (directive.$remove !== undefined) {
    if (!Array.isArray(directive.$remove)) {
      issues.push({
        severity: 'error',
        code: 'invalid-list-op',
        path,
        message: `$remove at '${path}' must be an array`,
        source: sourceAt(layer, path),
      });
    } else {
      items = items.filter(
        (item) => !directive.$remove!.some((target) => deepEqual(item.value, target)),
      );
    }
  }

  if (directive.$append !== undefined) {
    if (!Array.isArray(directive.$append)) {
      issues.push({
        severity: 'error',
        code: 'invalid-list-op',
        path,
        message: `$append at '${path}' must be an array`,
        source: sourceAt(layer, path),
      });
    } else {
      for (const value of directive.$append) {
        items.push({
          value,
          node: buildNode(path, value, { source: sourceAt(layer, path), value }, layer),
        });
      }
    }
  }
  return items;
}

function arrayFromNode(node: ProvNode, value: unknown): Item[] {
  if (!Array.isArray(value)) return [];
  return value.map((item, i) => ({
    value: item,
    node: node.children.get(String(i)) ?? makeNode(),
  }));
}

function reindex(items: Item[], node: ProvNode): void {
  node.children.clear();
  items.forEach((item, i) => node.children.set(String(i), item.node));
}

function mergeValue(
  path: string,
  baseValue: unknown,
  baseNode: ProvNode | undefined,
  incValue: unknown,
  layer: Layer,
  issues: MergeIssue[],
): { value: unknown; node: ProvNode } {
  const entry: ProvEntry = { source: sourceAt(layer, path), value: incValue };
  const directive = asDirective(incValue);

  if (directive !== undefined) {
    if (baseValue !== undefined && !Array.isArray(baseValue)) {
      issues.push({
        severity: 'error',
        code: 'invalid-list-op',
        path,
        message: `list directive at '${path}' applied to a non-array base value`,
        source: entry.source,
      });
      return { value: baseValue, node: baseNode ?? makeNode() };
    }
    const node = makeNode();
    node.entries.push(entry, ...(baseNode?.entries ?? []));
    const items = applyDirective(
      path,
      baseNode ? arrayFromNode(baseNode, baseValue) : [],
      directive,
      layer,
      issues,
    );
    reindex(items, node);
    return { value: items.map((i) => i.value), node };
  }

  if (isPlainObject(incValue) && isPlainObject(baseValue) && baseNode !== undefined) {
    const node = baseNode;
    node.entries.unshift(entry);
    for (const [k, v] of Object.entries(incValue)) {
      const childPath = path === '' ? k : `${path}.${k}`;
      const merged = mergeValue(
        childPath,
        (baseValue as Record<string, unknown>)[k],
        node.children.get(k),
        v,
        layer,
        issues,
      );
      (baseValue as Record<string, unknown>)[k] = merged.value;
      node.children.set(k, merged.node);
    }
    return { value: baseValue, node };
  }

  // Wholesale replace: scalars, plain arrays, and type changes.
  const node = buildNode(path, incValue, entry, layer);
  if (baseNode !== undefined) {
    node.entries.push(...baseNode.entries);
  }
  return { value: incValue, node };
}

export function mergeLayers(layers: Layer[]): MergeResult {
  const issues: MergeIssue[] = [];
  let value: unknown = undefined;
  let node: ProvNode | undefined;

  for (const layer of layers) {
    issues.push(...(layer.issues ?? []));
    if (layer.value === undefined) continue;
    const merged = mergeValue('', value, node, layer.value, layer, issues);
    value = merged.value;
    node = merged.node;
  }

  return { value, provenance: node ?? makeNode(), issues };
}
