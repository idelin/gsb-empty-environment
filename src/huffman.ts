import { BitReader, BitWriter } from './bitstream.ts';

export interface HuffmanCode {
  code: number;
  length: number;
}

interface TreeNode {
  freq: number;
  minSymbol: number;
  symbol: number; // -1 表示内部节点
  left: TreeNode | null;
  right: TreeNode | null;
}

const MAX_CODE_LENGTH = 32;

/** 由符号序列统计频率并构建霍夫曼码表（确定性：同频率按符号值排序合并）。 */
export function buildCodebook(symbols: ArrayLike<number>): Map<number, HuffmanCode> {
  const freq = new Map<number, number>();
  for (let i = 0; i < symbols.length; i += 1) {
    const s = symbols[i];
    freq.set(s, (freq.get(s) ?? 0) + 1);
  }
  if (freq.size === 0) {
    throw new Error('cannot build codebook from empty input');
  }
  const nodes: TreeNode[] = [...freq.entries()].map(([symbol, f]) => ({
    freq: f,
    minSymbol: symbol,
    symbol,
    left: null,
    right: null,
  }));
  while (nodes.length > 1) {
    nodes.sort((a, b) => a.freq - b.freq || a.minSymbol - b.minSymbol);
    const left = nodes.shift() as TreeNode;
    const right = nodes.shift() as TreeNode;
    nodes.push({
      freq: left.freq + right.freq,
      minSymbol: Math.min(left.minSymbol, right.minSymbol),
      symbol: -1,
      left,
      right,
    });
  }
  const codebook = new Map<number, HuffmanCode>();
  const root = nodes[0];
  if (root.symbol >= 0) {
    // 只有一个不同符号的退化情形：赋予 1 比特编码
    codebook.set(root.symbol, { code: 0, length: 1 });
    return codebook;
  }
  const walk = (node: TreeNode, code: number, length: number): void => {
    if (node.symbol >= 0) {
      if (length > MAX_CODE_LENGTH) {
        throw new Error(`huffman code length ${length} exceeds ${MAX_CODE_LENGTH} bits`);
      }
      codebook.set(node.symbol, { code, length });
      return;
    }
    walk(node.left as TreeNode, code << 1, length + 1);
    walk(node.right as TreeNode, (code << 1) | 1, length + 1);
  };
  walk(root, 0, 0);
  return codebook;
}

export interface DecodeNode {
  symbol: number; // -1 表示内部节点
  zero: DecodeNode | null;
  one: DecodeNode | null;
}

export function buildDecodeTree(codebook: Map<number, HuffmanCode>): DecodeNode {
  const root: DecodeNode = { symbol: -1, zero: null, one: null };
  for (const [symbol, { code, length }] of codebook) {
    let node = root;
    for (let i = length - 1; i >= 0; i -= 1) {
      const bit = (code >>> i) & 1;
      if (bit === 0) {
        if (!node.zero) node.zero = { symbol: -1, zero: null, one: null };
        node = node.zero;
      } else {
        if (!node.one) node.one = { symbol: -1, zero: null, one: null };
        node = node.one;
      }
    }
    node.symbol = symbol;
  }
  return root;
}

/**
 * 解码单个符号。
 * 码树不是满树时，损坏的比特会走到空分支而抛出异常——这是错误检测的第一道防线。
 */
export function decodeSymbol(reader: BitReader, root: DecodeNode): number {
  let node = root;
  while (node.symbol < 0) {
    const bit = reader.readBit();
    const next = bit === 0 ? node.zero : node.one;
    if (!next) {
      throw new Error('invalid huffman code (corrupted stream)');
    }
    node = next;
  }
  return node.symbol;
}

/** 码表序列化：u16 符号数 + 每符号 [u8 符号值, u8 码长, 码比特]。 */
export function serializeCodebook(writer: BitWriter, codebook: Map<number, HuffmanCode>): void {
  writer.writeBits(codebook.size, 16);
  const entries = [...codebook.entries()].sort((a, b) => a[0] - b[0]);
  for (const [symbol, { code, length }] of entries) {
    writer.writeBits(symbol, 8);
    writer.writeBits(length, 8);
    writer.writeBits(code, length);
  }
}

export function deserializeCodebook(reader: BitReader): Map<number, HuffmanCode> {
  const count = reader.readBits(16);
  const codebook = new Map<number, HuffmanCode>();
  for (let i = 0; i < count; i += 1) {
    const symbol = reader.readBits(8);
    const length = reader.readBits(8);
    const code = reader.readBits(length);
    codebook.set(symbol, { code, length });
  }
  return codebook;
}
