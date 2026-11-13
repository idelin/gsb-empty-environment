import { decodeBase64Url, encodeBase64Url } from "./base64url.ts";
import { bytesEqual, concatBytes, copyBytes, toHex } from "./bytes.ts";
import {
  type CborValue,
  decodeCborCanonical,
  encodeCborCanonical,
} from "./cbor.ts";
import { ChallengeParseError } from "./errors.ts";
import { findPrivateTokenChallenge } from "./header.ts";
import {
  parseHttpsOrigin,
  type RequestContext,
  verifyChallengeOrigin,
} from "./origin.ts";

export const TOKEN_TYPE_VOPRF = 0x0001;

const PRIVATE_TOKEN_CONTEXT_PREFIX = Uint8Array.of(0x00, 0x01);
const REDEMPTION_CONTEXT_LENGTH = 32;
const TOKEN_CHALLENGE_FIELDS = 4;

export interface TokenChallenge {
  readonly tokenType: number;
  readonly issuerName: string;
  readonly redemptionContext: Uint8Array;
  readonly originInfo: readonly string[];
}

export interface ParsedTokenChallenge extends TokenChallenge {
  readonly raw: Uint8Array;
}

export interface ChallengeDigest {
  readonly bytes: Uint8Array;
  readonly hex: string;
}

export function parseTokenChallenge(input: Uint8Array): ParsedTokenChallenge {
  const decoded = decodeCborCanonical(input);
  if (!Array.isArray(decoded) || decoded.length !== TOKEN_CHALLENGE_FIELDS) {
    throw new ChallengeParseError(
      "TokenChallenge must be a CBOR array with exactly four elements",
    );
  }

  const [tokenTypeValue, issuerValue, redemptionValue, originValue] = decoded;

  if (typeof tokenTypeValue !== "number" || !Number.isInteger(tokenTypeValue)) {
    throw new ChallengeParseError("TokenChallenge token_type must be a CBOR unsigned integer");
  }
  if (typeof issuerValue !== "string" || issuerValue.length === 0) {
    throw new ChallengeParseError("TokenChallenge issuer_name must be a non-empty text string");
  }
  if (!(redemptionValue instanceof Uint8Array)) {
    throw new ChallengeParseError("TokenChallenge redemption_context must be a CBOR byte string");
  }
  if (
    redemptionValue.length !== 0 &&
    redemptionValue.length !== REDEMPTION_CONTEXT_LENGTH
  ) {
    throw new ChallengeParseError(
      "TokenChallenge redemption_context must be empty or exactly 32 bytes",
    );
  }
  if (!Array.isArray(originValue)) {
    throw new ChallengeParseError("TokenChallenge origin_info must be a CBOR array");
  }

  const originInfo: string[] = [];
  for (const entry of originValue) {
    if (typeof entry !== "string") {
      throw new ChallengeParseError("Every origin_info entry must be a text string");
    }
    parseHttpsOrigin(entry);
    originInfo.push(entry);
  }

  const challenge: TokenChallenge = {
    tokenType: tokenTypeValue,
    issuerName: issuerValue,
    redemptionContext: copyBytes(redemptionValue),
    originInfo,
  };

  const reEncoded = encodeTokenChallenge(challenge);
  if (!bytesEqual(reEncoded, input)) {
    throw new ChallengeParseError(
      "TokenChallenge is not in canonical encoding: re-encoding does not match the original bytes",
    );
  }

  return { ...challenge, raw: copyBytes(input) };
}

export function parseTokenChallengeBase64Url(challenge: string): ParsedTokenChallenge {
  return parseTokenChallenge(decodeBase64Url(challenge));
}

export function parsePrivateTokenChallengeHeader(header: string): ParsedTokenChallenge {
  return parseTokenChallengeBase64Url(findPrivateTokenChallenge(header));
}

export function encodeTokenChallenge(challenge: TokenChallenge): Uint8Array {
  validateForEncoding(challenge);
  const value: CborValue[] = [
    challenge.tokenType,
    challenge.issuerName,
    challenge.redemptionContext,
    [...challenge.originInfo],
  ];
  return encodeCborCanonical(value);
}

export function encodeTokenChallengeBase64Url(challenge: TokenChallenge): string {
  return encodeBase64Url(encodeTokenChallenge(challenge));
}

export async function computeChallengeDigest(
  challenge: TokenChallenge | ParsedTokenChallenge,
): Promise<ChallengeDigest> {
  const raw = isParsed(challenge)
    ? challenge.raw
    : encodeTokenChallenge(challenge);
  const digestInput = concatBytes([PRIVATE_TOKEN_CONTEXT_PREFIX, raw]);
  const subtle = getSubtleCrypto();
  const digest = new Uint8Array(await subtle.digest("SHA-256", digestInput));
  return { bytes: digest, hex: toHex(digest) };
}

export async function handlePrivateTokenChallenge(
  header: string,
  context: RequestContext,
): Promise<{ challenge: ParsedTokenChallenge; digest: ChallengeDigest }> {
  const challenge = parsePrivateTokenChallengeHeader(header);
  verifyChallengeOrigin(challenge.originInfo, context);
  const digest = await computeChallengeDigest(challenge);
  return { challenge, digest };
}

function validateForEncoding(challenge: TokenChallenge): void {
  if (!Number.isSafeInteger(challenge.tokenType) || challenge.tokenType < 0) {
    throw new ChallengeParseError("token_type must be a non-negative safe integer");
  }
  if (challenge.issuerName.length === 0) {
    throw new ChallengeParseError("issuer_name must not be empty");
  }
  if (
    challenge.redemptionContext.length !== 0 &&
    challenge.redemptionContext.length !== REDEMPTION_CONTEXT_LENGTH
  ) {
    throw new ChallengeParseError("redemption_context must be empty or exactly 32 bytes");
  }
  for (const entry of challenge.originInfo) {
    parseHttpsOrigin(entry);
  }
}

function isParsed(challenge: TokenChallenge | ParsedTokenChallenge): challenge is ParsedTokenChallenge {
  return "raw" in challenge && challenge.raw instanceof Uint8Array;
}

function getSubtleCrypto(): SubtleCrypto {
  const cryptoObject = globalThis.crypto;
  if (!cryptoObject?.subtle) {
    throw new ChallengeParseError(
      "Web Crypto SubtleCrypto is required to compute the challenge digest",
    );
  }
  return cryptoObject.subtle;
}
