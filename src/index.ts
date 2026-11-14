export * from "./errors.ts";
export * from "./parser.ts";
export * from "./number.ts";
export * from "./canonical.ts";
export * from "./normalize.ts";
export * from "./gateway.ts";
export * from "./gateway-types.ts";

import {
  canonicalizeNumber,
  exactNumberToSafeValue,
} from "./number.ts";
import {
  admitNormalized,
  admitRequest,
  createAdmissionGate,
  createAdmissionMiddleware,
  createGateway,
  createRequestGate,
  evaluateWaf,
  normalizedBody,
  routeRequest,
} from "./gateway.ts";
import {
  computeDigest,
  hmacSha256Hex,
  normalizeRequest,
  normalizeSignatureValue,
  sha256Hex,
  signNormalized,
  tryNormalizeRequest,
  verifyNormalizedSignature,
} from "./normalize.ts";
import { parseJsonOnce } from "./parser.ts";

export const normalize = normalizeRequest;
export const normalizeJsonRequestBody = normalizeRequest;
export const normalizeRequestSafe = tryNormalizeRequest;
export const parseRequestJson = parseJsonOnce;
export const parseJson = parseJsonOnce;
export const admit = admitRequest;
export const decideRequest = admitRequest;
export const decide = admitRequest;
export const guardRequest = admitRequest;
export const evaluateRequest = admitRequest;
export const evaluate = admitRequest;
export const wafDecision = evaluateWaf;
export const routerDecision = routeRequest;
export const signRequest = signNormalized;
export const signCanonical = signNormalized;
export const verifySignature = verifyNormalizedSignature;

const api = Object.freeze({
  parseJsonOnce,
  parseJson: parseJsonOnce,
  normalizeRequest,
  normalize: normalizeRequest,
  normalizeJson: normalizeRequest,
  admit: admitRequest,
  admitRequest,
  admitNormalized,
  evaluateWaf,
  routeRequest,
  createRequestGate,
  createGateway,
  createAdmissionGate,
  createGate: createRequestGate,
  createAdmissionMiddleware,
  normalizedBody,
  computeDigest,
  signNormalized,
  signRequest: signNormalized,
  verifyNormalizedSignature,
  verifySignature: verifyNormalizedSignature,
  normalizeSignatureValue,
  canonicalizeNumber,
  exactNumberToSafeValue,
  sha256Hex,
  hmacSha256Hex,
  tryNormalizeRequest,
});

export default api;
