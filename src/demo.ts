/**
 * Demo: corrupt a Huffman bitstream by deleting 3 bits, then compare a naive
 * decoder against the fault-tolerant sync-word decoder.
 */
import { encode, decodeNaive, decodeTolerant, SEGMENT_BYTES, SEGMENT_BITS } from "./codec.ts";
import { deleteBits } from "./bitstream.ts";
import { SAMPLE_MESSAGE, sampleCodebook, messageToSymbols, symbolsToMessage } from "./sample.ts";

const DELETE_AT_BIT = 400; // byte 50, middle of segment 0
const DELETE_COUNT = 3;

const book = sampleCodebook();
const symbols = messageToSymbols(SAMPLE_MESSAGE);
const encoded = encode(symbols, book);

console.log("=== Fault-tolerant Huffman stream demo ===");
console.log(`message: ${symbols.length} symbols, encoded: ${Math.ceil(encoded.bits.length / 8)} bytes, ` +
  `segments of ${SEGMENT_BYTES} bytes, sync words at bytes ${encoded.syncBitPositions.slice(0, 4).map((p) => p / 8).join(", ")}...`);

// --- corrupt the channel: wipe 3 bits at byte 50 ---
const corrupted = deleteBits(encoded.bits, DELETE_AT_BIT, DELETE_COUNT);
console.log(`\nchannel: deleted ${DELETE_COUNT} bits at byte ${DELETE_AT_BIT / 8} (bit ${DELETE_AT_BIT})`);

// --- ordinary decoder ---
const naiveSymbols = decodeNaive(corrupted, book);
const naiveText = symbolsToMessage(naiveSymbols);
console.log(`\n[naive decoder]  decoded ${naiveText.length} symbols`);
console.log(`  head: ${JSON.stringify(naiveText.slice(0, 60))}`);
console.log(`  tail after corruption: ${JSON.stringify(naiveText.slice(60, 160))}`);

// --- fault-tolerant decoder ---
const result = decodeTolerant(corrupted, book);
const resyncBit = result.syncBitPositions[1];
console.log(`\n[fault-tolerant decoder] decoded ${result.symbols.length} symbols, resync events: ${result.resyncEvents}`);
console.log(`  re-locked on sync word at corrupted-stream bit ${resyncBit} ` +
  `(original byte ${(resyncBit + DELETE_COUNT) / 8}, i.e. the sync word at byte ${SEGMENT_BYTES})`);

// --- damage assessment ---
const lens = symbols.map((s) => book.get(s)!.length);
const intactPrefix = encoded.symbolBitPositions.filter((p, i) => p + lens[i] <= DELETE_AT_BIT).length;
const seg1Start = encoded.symbolBitPositions.filter((p) => p < SEGMENT_BITS).length;
const lost = seg1Start - intactPrefix;
const expectedTail = symbols.slice(seg1Start);
const gotTail = result.symbols.slice(result.symbols.length - expectedTail.length);
const tailOk = gotTail.length === expectedTail.length && gotTail.every((s, i) => s === expectedTail[i]);
const recovered = expectedTail.length;
const atRisk = symbols.length - intactPrefix;
let naiveMatch = 0;
for (let i = intactPrefix; i < Math.min(naiveSymbols.length, symbols.length); i++) {
  if (naiveSymbols[i] === symbols[i]) naiveMatch++;
}

console.log(`\n  symbols intact before corruption: ${intactPrefix}`);
console.log(`  corrupted symbols skipped:        ${lost}`);
console.log(`  symbols recovered after resync:   ${recovered} / ${atRisk} post-corruption symbols`);
console.log(`  recovery rate:                    ${(100 * recovered / atRisk).toFixed(1)}%`);
console.log(`  recovered tail identical to original: ${tailOk}`);
console.log(`\n  naive decoder post-corruption exact-match rate: ${(100 * naiveMatch / atRisk).toFixed(1)}% (rest is garbled/misaligned)`);

const tolerantText = symbolsToMessage(result.symbols.slice(0, intactPrefix)) +
  `  ⟦…${lost} corrupted symbols skipped…⟧  ` +
  symbolsToMessage(gotTail);
console.log(`\n  rebuilt message around the damage:\n  ${JSON.stringify(tolerantText.slice(40, 220))}`);
