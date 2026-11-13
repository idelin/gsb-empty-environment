import { ChallengeParseError } from "./errors.ts";

const DECODE_TABLE = buildDecodeTable();

export function encodeBase64Url(input: Uint8Array): string {
  let output = "";
  let bits = 0;
  let bitCount = 0;

  for (const byte of input) {
    bits = (bits << 8) | byte;
    bitCount += 8;
    while (bitCount >= 6) {
      bitCount -= 6;
      output += base64UrlChar((bits >> bitCount) & 0x3f);
    }
  }

  if (bitCount > 0) {
    output += base64UrlChar((bits << (6 - bitCount)) & 0x3f);
  }

  return output;
}

export function decodeBase64Url(input: string): Uint8Array {
  const compact = stripPadding(input);
  if (compact.length === 0) {
    return new Uint8Array(0);
  }

  const output: number[] = [];
  let bits = 0;
  let bitCount = 0;

  for (let index = 0; index < compact.length; index += 1) {
    const value = DECODE_TABLE[compact.charCodeAt(index)];
    if (value === undefined) {
      throw new ChallengeParseError(
        "Challenge parameter contains an invalid base64url character",
      );
    }
    bits = (bits << 6) | value;
    bitCount += 6;
    if (bitCount >= 8) {
      bitCount -= 8;
      output.push((bits >> bitCount) & 0xff);
    }
  }

  if (bitCount >= 6 || (bits & ((1 << bitCount) - 1)) !== 0) {
    throw new ChallengeParseError("Challenge parameter contains trailing bits");
  }

  return Uint8Array.from(output);
}

function stripPadding(input: string): string {
  const firstPadding = input.indexOf("=");
  if (firstPadding === -1) {
    return input;
  }
  const compact = input.slice(0, firstPadding);
  for (let index = firstPadding; index < input.length; index += 1) {
    if (input.charCodeAt(index) !== 61) {
      throw new ChallengeParseError("Base64url padding is malformed");
    }
  }
  return compact;
}

function buildDecodeTable(): number[] {
  const table: number[] = [];
  for (let value = 0; value < 64; value += 1) {
    table[base64UrlChar(value).charCodeAt(0)] = value;
  }
  return table;
}

function base64UrlChar(value: number): string {
  if (value < 26) {
    return String.fromCharCode(65 + value);
  }
  if (value < 52) {
    return String.fromCharCode(97 + (value - 26));
  }
  if (value < 62) {
    return String.fromCharCode(48 + (value - 52));
  }
  return value === 62 ? "-" : "_";
}
