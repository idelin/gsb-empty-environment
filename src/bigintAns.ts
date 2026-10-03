import { ALPHABET_SIZE, countFrequencies, normalizeCounts, DEFAULT_TABLE_LOG } from "./normalize.ts";

/**
 * Pure ANS on a single unbounded integer (rANS without renormalization).
 *
 * This is the conceptual core of the whole family: the ENTIRE message is
 * folded into one big integer `state`. Push/Pop are exact inverses:
 *
 *   push(s): x <- floor(x / f_s) * M + cumul[s] + (x mod f_s)
 *   pop():   s = symbolOf(x mod M);  x <- f_s * floor(x / M) + (x mod M) - cumul[s]
 *
 * The streaming tANS codec in tans.ts is exactly this machine plus
 * renormalization that spills low bits to the byte stream to keep the state
 * bounded. Here we let the BigInt grow, which is didactic and exact but
 * O(message length) per step — hence tANS for real work.
 */

export interface BigIntAnsModel {
  tableLog: number;
  modulus: bigint;
  norm: Uint32Array;
  cumul: Uint32Array;
  slotToSymbol: Uint8Array;
}

export function buildModel(norm: Uint32Array, tableLog: number): BigIntAnsModel {
  const tableSize = 1 << tableLog;
  const cumul = new Uint32Array(ALPHABET_SIZE + 1);
  for (let s = 0; s < ALPHABET_SIZE; s++) cumul[s + 1] = cumul[s] + norm[s];
  if (cumul[ALPHABET_SIZE] !== tableSize) {
    throw new Error("frequencies must sum to 2^tableLog");
  }
  const slotToSymbol = new Uint8Array(tableSize);
  for (let s = 0; s < ALPHABET_SIZE; s++) {
    slotToSymbol.fill(s, cumul[s], cumul[s + 1]);
  }
  return { tableLog, modulus: 1n << BigInt(tableLog), norm, cumul, slotToSymbol };
}

export class BigIntAnsEncoder {
  state: bigint;
  model: BigIntAnsModel;

  constructor(model: BigIntAnsModel) {
    this.model = model;
    this.state = model.modulus; // any x >= 1 works; M keeps states aligned with tANS
  }

  push(symbol: number): void {
    const m = this.model;
    const f = BigInt(m.norm[symbol]);
    if (f === 0n) throw new Error(`symbol ${symbol} not in model`);
    this.state =
      (this.state / f) * m.modulus + BigInt(m.cumul[symbol]) + (this.state % f);
  }
}

export class BigIntAnsDecoder {
  state: bigint;
  model: BigIntAnsModel;

  constructor(state: bigint, model: BigIntAnsModel) {
    this.state = state;
    this.model = model;
  }

  pop(): number {
    const m = this.model;
    const slot = Number(this.state % m.modulus);
    const symbol = m.slotToSymbol[slot];
    this.state =
      BigInt(m.norm[symbol]) * (this.state / m.modulus) +
      BigInt(slot - m.cumul[symbol]);
    return symbol;
  }
}

/** Convenience: fold a whole message into one big integer. */
export function encodeToBigInt(
  data: Uint8Array,
  tableLog = DEFAULT_TABLE_LOG,
): { state: bigint; model: BigIntAnsModel } {
  const norm = normalizeCounts(countFrequencies(data), tableLog);
  const model = buildModel(norm, tableLog);
  const enc = new BigIntAnsEncoder(model);
  for (let i = data.length - 1; i >= 0; i--) enc.push(data[i]);
  return { state: enc.state, model };
}

export function decodeFromBigInt(
  state: bigint,
  model: BigIntAnsModel,
  length: number,
): Uint8Array {
  const dec = new BigIntAnsDecoder(state, model);
  const out = new Uint8Array(length);
  for (let i = 0; i < length; i++) out[i] = dec.pop();
  return out;
}
