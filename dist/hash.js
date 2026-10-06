"use strict";
Object.defineProperty(exports, "__esModule", { value: true });

const FNV1A_32_OFFSET_BASIS = 0x811c9dc5;
const FNV1A_32_PRIME = 0x01000193;
const BUCKET_COUNT = 10000;

function utf8Bytes(input) {
  return new TextEncoder().encode(input);
}

function fnv1a32(bytes) {
  let hash = FNV1A_32_OFFSET_BASIS;
  for (let i = 0; i < bytes.length; i++) {
    hash ^= bytes[i];
    hash = Math.imul(hash, FNV1A_32_PRIME) >>> 0;
  }
  return hash >>> 0;
}

function hashString(input) {
  return fnv1a32(utf8Bytes(input));
}

const KEY_SEPARATOR = "\u001f";

function bucketKey(parts) {
  return parts.join(KEY_SEPARATOR);
}

function bucketOf(key) {
  return hashString(key) % BUCKET_COUNT;
}

module.exports = {
  FNV1A_32_OFFSET_BASIS,
  FNV1A_32_PRIME,
  BUCKET_COUNT,
  utf8Bytes,
  fnv1a32,
  hashString,
  bucketKey,
  bucketOf,
};
