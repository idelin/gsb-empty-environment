export * from './types.ts';
export * from './snapshot.ts';
export * from './selector.ts';
export * from './number.ts';
export * from './currency.ts';
export * from './extract.ts';

import {
  analyzePrices,
  createPriceExtractor,
  extractPrice,
  extractPriceEvidence,
  extractPrices,
  extractProductPrices,
  PriceConfigError,
} from './extract.ts';
import {
  parseAmount,
  parseAmountText,
  parsePrice,
  parsePriceText,
  parsePriceValue,
  scanAmounts,
} from './number.ts';
import { normalizeSnapshot } from './snapshot.ts';
import { matchesSelector } from './selector.ts';

const api = {
  analyzePrices,
  createPriceExtractor,
  extractPrice,
  extractPriceEvidence,
  extractPrices,
  extractProductPrices,
  matchesSelector,
  normalizeSnapshot,
  parseAmountText,
  parseAmount,
  parsePrice,
  parsePriceText,
  parsePriceValue,
  scanAmounts,
  PriceConfigError,
};

export default api;
