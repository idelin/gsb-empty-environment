import { createHash } from 'node:crypto';
import type { Nibbles } from './nibbles.ts';
import { nibblesToHex } from './nibbles.ts';

export interface RangeSummary {
  count: number;
  minKey: string | null;
  maxKey: string | null;
}

export const EMPTY_SUMMARY: RangeSummary = { count: 0, minKey: null, maxKey: null };

export function mergeSummaries(summaries: RangeSummary[]): RangeSummary {
  let count = 0;
  let minKey: string | null = null;
  let maxKey: string | null = null;
  for (const s of summaries) {
    count += s.count;
    if (s.minKey !== null && (minKey === null || s.minKey < minKey)) minKey = s.minKey;
    if (s.maxKey !== null && (maxKey === null || s.maxKey > maxKey)) maxKey = s.maxKey;
  }
  return { count, minKey, maxKey };
}

function sha256(data: string): string {
  return createHash('sha256').update(data).digest('hex');
}

export function valueDigest(value: Buffer): string {
  return createHash('sha256').update(value).digest('hex');
}

function summaryDigest(s: RangeSummary): string {
  return `${s.count}|${s.minKey ?? ''}|${s.maxKey ?? ''}`;
}

export function hashLeaf(pathHex: string, key: string, value: Buffer, summary: RangeSummary): string {
  return sha256(['leaf', pathHex, key, valueDigest(value), summaryDigest(summary)].join('|'));
}

export function hashExtension(pathHex: string, childHash: string, summary: RangeSummary): string {
  return sha256(['extension', pathHex, childHash, summaryDigest(summary)].join('|'));
}

export function hashBranch(
  childHashes: (string | null)[],
  value: Buffer | null,
  key: string | null,
  summary: RangeSummary,
): string {
  const slots = childHashes.map((h) => (h === null ? '-' : h)).join(',');
  return sha256([
    'branch',
    slots,
    value === null ? '-' : valueDigest(value),
    key ?? '-',
    summaryDigest(summary),
  ].join('|'));
}

export abstract class TrieNode {
  abstract readonly kind: 'leaf' | 'extension' | 'branch';
  abstract readonly summary: RangeSummary;
  abstract readonly hash: string;
}

export class LeafNode extends TrieNode {
  readonly kind = 'leaf' as const;
  readonly path: Nibbles;
  readonly key: string;
  readonly value: Buffer;
  readonly summary: RangeSummary;
  readonly hash: string;

  constructor(path: Nibbles, key: string, value: Buffer) {
    super();
    this.path = path;
    this.key = key;
    this.value = value;
    this.summary = { count: 1, minKey: key, maxKey: key };
    this.hash = hashLeaf(nibblesToHex(path), key, value, this.summary);
  }
}

export class ExtensionNode extends TrieNode {
  readonly kind = 'extension' as const;
  readonly path: Nibbles;
  readonly child: TrieNode;
  readonly summary: RangeSummary;
  readonly hash: string;

  constructor(path: Nibbles, child: TrieNode) {
    super();
    if (path.length === 0) throw new Error('extension path must not be empty');
    this.path = path;
    this.child = child;
    this.summary = child.summary;
    this.hash = hashExtension(nibblesToHex(path), child.hash, this.summary);
  }
}

export class BranchNode extends TrieNode {
  readonly kind = 'branch' as const;
  readonly children: (TrieNode | null)[];
  readonly value: Buffer | null;
  readonly key: string | null;
  readonly summary: RangeSummary;
  readonly hash: string;

  constructor(children: (TrieNode | null)[], value: Buffer | null = null, key: string | null = null) {
    super();
    if (children.length !== 16) throw new Error('branch node requires exactly 16 slots');
    this.children = children;
    this.value = value;
    this.key = key;
    const parts: RangeSummary[] = [];
    for (const child of children) {
      if (child !== null) parts.push(child.summary);
    }
    if (value !== null && key !== null) {
      parts.push({ count: 1, minKey: key, maxKey: key });
    }
    this.summary = mergeSummaries(parts);
    this.hash = hashBranch(
      children.map((c) => (c === null ? null : c.hash)),
      value,
      key,
      this.summary,
    );
  }
}
