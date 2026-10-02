/**
 * Fault-tolerant Huffman framing codec.
 *
 * The bitstream is split into fixed-size segments of SEGMENT_BYTES bytes.
 * Each segment starts with a 32-bit self-synchronizing sync word, followed by
 * Huffman-coded symbols, an EOS marker, and zero padding to the segment boundary.
 *
 * A naive decoder trusts the fixed layout and loses phase forever after a bit
 * deletion. The fault-tolerant decoder scans for the next sync word at any bit
 * offset, re-locks phase there, and isolates the damage to one segment.
 */
import { buildTrie, decodeSymbol, type Code, type TrieNode } from "./huffman.ts";

export const SYNC_WORD = 0x1acffc1d;
export const SYNC_BITS = 32;
export const SEGMENT_BYTES = 100;
export const SEGMENT_BITS = SEGMENT_BYTES * 8;
export const EOS = 256; // virtual end-of-segment symbol

export interface EncodeResult {
  bits: Uint8Array;
  /** Bit offset where each input symbol's codeword starts. */
  symbolBitPositions: number[];
  /** Bit offset of every sync word. */
  syncBitPositions: number[];
}

export function encode(symbols: number[], book: Map<number, Code>): EncodeResult {
  const eos = book.get(EOS);
  if (!eos) throw new Error("codebook must contain the EOS symbol");
  const dataBits = SEGMENT_BITS - SYNC_BITS;
  const bits: number[] = [];
  const symbolBitPositions: number[] = [];
  const syncBitPositions: number[] = [];

  const pushBits = (value: number, length: number): void => {
    for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };

  let i = 0;
  while (i < symbols.length) {
    syncBitPositions.push(bits.length);
    pushBits(SYNC_WORD, SYNC_BITS);
    let used = 0;
    while (i < symbols.length) {
      const c = book.get(symbols[i]);
      if (!c) throw new Error(`symbol ${symbols[i]} missing from codebook`);
      if (used + c.length + eos.length > dataBits) break; // keep room for EOS
      symbolBitPositions.push(bits.length);
      pushBits(c.code, c.length);
      used += c.length;
      i++;
    }
    pushBits(eos.code, eos.length);
    while (bits.length % SEGMENT_BITS !== 0) bits.push(0); // pad to segment boundary
  }
  return { bits: Uint8Array.from(bits), symbolBitPositions, syncBitPositions };
}

/** Bit-level search for the sync word at any offset, starting from bit `from`. */
export function findSync(bits: Uint8Array, from: number): number {
  outer: for (let i = from; i + SYNC_BITS <= bits.length; i++) {
    for (let j = 0; j < SYNC_BITS; j++) {
      if (bits[i + j] !== ((SYNC_WORD >>> (SYNC_BITS - 1 - j)) & 1)) continue outer;
    }
    return i;
  }
  return -1;
}

/**
 * Ordinary decoder: trusts the fixed segment layout. Any bit deletion shifts
 * every subsequent segment boundary and turns the rest of the stream to garbage.
 */
export function decodeNaive(bits: Uint8Array, book: Map<number, Code>): number[] {
  const trie = buildTrie(book);
  const symbols: number[] = [];
  for (let segStart = 0; segStart + SYNC_BITS <= bits.length; segStart += SEGMENT_BITS) {
    let pos = segStart + SYNC_BITS;
    const segEnd = Math.min(segStart + SEGMENT_BITS, bits.length);
    while (pos < segEnd) {
      const d = decodeSymbol(trie, bits, pos);
      if (!d) {
        pos++; // undecodable: stumble forward one bit
        continue;
      }
      pos += d.length;
      if (d.symbol === EOS) break;
      symbols.push(d.symbol);
    }
  }
  return symbols;
}

export interface TolerantResult {
  symbols: number[];
  /** Bit offsets where sync words were (re-)acquired. */
  syncBitPositions: number[];
  /** Number of segments whose decode failed and triggered a resync scan. */
  resyncEvents: number;
}

/**
 * Fault-tolerant decoder: hunts for sync words at any bit offset. After a bit
 * error or deletion it skips the corrupted symbols and re-locks phase at the
 * next sync word, isolating the damage to a single segment.
 */
export function decodeTolerant(bits: Uint8Array, book: Map<number, Code>): TolerantResult {
  const trie = buildTrie(book);
  const symbols: number[] = [];
  const syncBitPositions: number[] = [];
  let resyncEvents = 0;
  let pos = 0;
  while (pos < bits.length) {
    const syncAt = findSync(bits, pos);
    if (syncAt === -1) break;
    syncBitPositions.push(syncAt);
    let p = syncAt + SYNC_BITS;
    const limit = Math.min(syncAt + SEGMENT_BITS, bits.length);
    let clean = false;
    while (p < limit) {
      const d = decodeSymbol(trie, bits, p);
      if (!d) break; // invalid codeword: corruption detected
      p += d.length;
      if (d.symbol === EOS) {
        clean = true;
        break;
      }
      symbols.push(d.symbol);
    }
    if (clean) {
      pos = p; // segment decoded cleanly; look for the next sync word
    } else {
      resyncEvents++;
      pos = syncAt + 1; // segment is corrupt: rescan for a phase-shifted sync word
    }
  }
  return { symbols, syncBitPositions, resyncEvents };
}
