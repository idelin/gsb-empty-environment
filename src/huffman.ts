/** Huffman codebook construction and symbol-level decode. */

export interface Code {
  code: number;   // right-aligned bits
  length: number; // number of significant bits
}

interface TreeNode {
  freq: number;
  symbol?: number;
  left?: TreeNode;
  right?: TreeNode;
}

export function buildCodebook(freqs: Map<number, number>): Map<number, Code> {
  const pq: TreeNode[] = [...freqs.entries()].map(([symbol, freq]) => ({ symbol, freq }));
  if (pq.length === 0) throw new Error("empty frequency table");
  while (pq.length > 1) {
    pq.sort((a, b) => a.freq - b.freq);
    const a = pq.shift()!;
    const b = pq.shift()!;
    pq.push({ freq: a.freq + b.freq, left: a, right: b });
  }
  const book = new Map<number, Code>();
  const walk = (n: TreeNode, code: number, length: number): void => {
    if (n.symbol !== undefined) {
      book.set(n.symbol, { code, length: Math.max(length, 1) });
      return;
    }
    walk(n.left!, code << 1, length + 1);
    walk(n.right!, (code << 1) | 1, length + 1);
  };
  walk(pq[0], 0, 0);
  return book;
}

export interface TrieNode {
  symbol?: number;
  zero?: TrieNode;
  one?: TrieNode;
}

export function buildTrie(book: Map<number, Code>): TrieNode {
  const root: TrieNode = {};
  for (const [symbol, { code, length }] of book) {
    let node = root;
    for (let i = length - 1; i >= 0; i--) {
      const bit = (code >>> i) & 1;
      if (bit) node = node.one ??= {};
      else node = node.zero ??= {};
    }
    node.symbol = symbol;
  }
  return root;
}

/**
 * Decode one symbol starting at bit `pos`.
 * Returns null when the bit pattern is not a valid codeword or runs past the end.
 */
export function decodeSymbol(
  trie: TrieNode,
  bits: Uint8Array,
  pos: number,
): { symbol: number; length: number } | null {
  let node: TrieNode | undefined = trie;
  let length = 0;
  while (node !== undefined && node.symbol === undefined) {
    if (pos + length >= bits.length) return null;
    node = bits[pos + length] ? node.one : node.zero;
    length++;
  }
  if (node === undefined || node.symbol === undefined) return null;
  return { symbol: node.symbol, length };
}
