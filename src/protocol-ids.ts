import { hashDomain } from "./canonical.ts";
import type { EmailTokenClaims } from "./types.ts";

export function deriveTransactionId(claims: EmailTokenClaims): string {
  return (
    "txn_" +
    hashDomain("ddd-cross-device/issuance-transaction/v1", {
      accountId: claims.accountId,
      challengeId: claims.challengeId,
      tokenId: claims.tokenId,
      browserId: claims.browserId,
      browserSessionId: claims.browserSessionId,
      browserKeyId: claims.browserKeyId,
      purpose: claims.purpose,
    })
  );
}
