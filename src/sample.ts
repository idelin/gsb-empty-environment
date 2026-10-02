/** Shared sample message + codebook used by the demo and the tests. */
import { buildCodebook, type Code } from "./huffman.ts";
import { EOS } from "./codec.ts";

export const SAMPLE_MESSAGE = (
  "the quick brown fox jumps over the lazy dog while the rain keeps falling " +
  "on the quiet town and the river carries the sound of the evening far away " +
  "toward the sea where the waves answer the wind in a language of their own "
).repeat(12);

export function sampleCodebook(message: string = SAMPLE_MESSAGE): Map<number, Code> {
  const freqs = new Map<number, number>();
  for (const byte of Buffer.from(message, "utf8")) {
    freqs.set(byte, (freqs.get(byte) ?? 0) + 1);
  }
  freqs.set(EOS, 1); // rare EOS marker -> long codeword, cheap padding
  return buildCodebook(freqs);
}

export function messageToSymbols(message: string): number[] {
  return [...Buffer.from(message, "utf8")];
}

export function symbolsToMessage(symbols: number[]): string {
  return Buffer.from(symbols).toString("utf8");
}
