import { deepFreeze, sha256 } from "./canonical.ts";
import type {
  AuthEvent,
  AuthState,
  AuthorizationAudit,
  BrowserRecord,
  ChallengeState,
  ChallengeStatus,
  ChallengeView,
  DeviceRecord,
  EmailToken,
  SignedReceipt,
} from "./types.ts";

export function emptyState(now = 0): AuthState {
  return {
    browsers: new Map(),
    devices: new Map(),
    challenges: new Map(),
    tokenToChallenge: new Map(),
    receiptsByHash: new Map(),
    lastReceiptHash: null,
    lastEventHash: null,
    eventCount: 0,
    now,
  };
}

export function browserKey(accountId: string, browserId: string): string {
  return `${accountId}\u0000${browserId}`;
}

export function deviceKey(accountId: string, deviceId: string): string {
  return `${accountId}\u0000${deviceId}`;
}

function rememberReceipt(state: AuthState, receipt: SignedReceipt | undefined): void {
  if (!receipt) return;
  state.receiptsByHash.set(receipt.hash, receipt);
  state.lastReceiptHash = receipt.hash;
}

export function applyEvent(state: AuthState, event: AuthEvent): void {
  switch (event.type) {
    case "browser.registered": {
      const { accountId, browserId, key, at } = event.data;
      const scopedKey = browserKey(accountId, browserId);
      const existing = state.browsers.get(scopedKey);
      if (existing) {
        existing.keys[key.keyId] = key;
        existing.activeKeyId = key.keyId;
      } else {
        state.browsers.set(scopedKey, {
          accountId,
          browserId,
          keys: { [key.keyId]: key },
          activeKeyId: key.keyId,
          createdAt: at,
        });
      }
      state.now = Math.max(state.now, at);
      break;
    }
    case "browser.key_rotated": {
      const { accountId, browserId, key, retiredKeyId, at } = event.data;
      const browser = state.browsers.get(browserKey(accountId, browserId));
      if (!browser) throw new Error(`Unknown browser ${browserId} during replay`);
      const retired = browser.keys[retiredKeyId];
      if (retired && retired.retiredAt === undefined) {
        browser.keys[retiredKeyId] = { ...retired, retiredAt: at };
      }
      browser.keys[key.keyId] = key;
      browser.activeKeyId = key.keyId;
      state.now = Math.max(state.now, at);
      break;
    }
    case "browser.revoked": {
      const { accountId, browserId, at } = event.data;
      const browser = state.browsers.get(browserKey(accountId, browserId));
      if (!browser) throw new Error(`Unknown browser ${browserId} during replay`);
      if (browser.revokedAt === undefined || at < browser.revokedAt) browser.revokedAt = at;
      browser.revocationReceipt = event.data.receipt;
      rememberReceipt(state, event.data.receipt);
      state.now = Math.max(state.now, at);
      break;
    }
    case "device.registered": {
      const { accountId, deviceId, keyId, publicKey, generation, at } = event.data;
      state.devices.set(deviceKey(accountId, deviceId), {
        accountId,
        deviceId,
        keyId,
        publicKey,
        generation,
        registeredAt: at,
      });
      state.now = Math.max(state.now, at);
      break;
    }
    case "device.replaced": {
      const { oldDeviceId, newDeviceId, keyId, publicKey, generation, at, accountId } =
        event.data;
      const old = state.devices.get(deviceKey(accountId, oldDeviceId));
      if (old) {
        old.revokedAt = at;
        old.replacedBy = newDeviceId;
      }
      state.devices.set(deviceKey(accountId, newDeviceId), {
        accountId,
        deviceId: newDeviceId,
        keyId,
        publicKey,
        generation,
        registeredAt: at,
      });
      rememberReceipt(state, event.data.receipt);
      state.now = Math.max(state.now, at);
      break;
    }
    case "device.revoked": {
      const { accountId, deviceId, at } = event.data;
      const device = state.devices.get(deviceKey(accountId, deviceId));
      if (device && device.revokedAt === undefined) device.revokedAt = at;
      state.now = Math.max(state.now, at);
      break;
    }
    case "login.started": {
      const { claims, token, tokenHash, dispatchReceipt, at } = event.data;
      if (state.challenges.has(claims.challengeId)) {
        throw new Error(`Duplicate challenge ${claims.challengeId}`);
      }
      if (state.tokenToChallenge.has(claims.tokenId)) {
        throw new Error(`Duplicate token ${claims.tokenId}`);
      }
      state.challenges.set(claims.challengeId, {
        tokenId: claims.tokenId,
        challengeId: claims.challengeId,
        claims,
        token,
        tokenHash,
        dispatchReceipt,
        opens: [],
      });
      state.tokenToChallenge.set(claims.tokenId, claims.challengeId);
      rememberReceipt(state, dispatchReceipt);
      state.now = Math.max(state.now, at);
      break;
    }
    case "email.delivered": {
      const { tokenId, deliveryId, deliveredAt, providerRef, receipt } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge) throw new Error(`Unknown token ${tokenId} during replay`);
      if (!challenge.delivery) {
        challenge.delivery = { receipt, deliveryId, providerRef, deliveredAt };
      }
      rememberReceipt(state, receipt);
      state.now = Math.max(state.now, deliveredAt);
      break;
    }
    case "email.opened": {
      const { tokenId, access } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge) throw new Error(`Unknown token ${tokenId} during replay`);
      if (!challenge.opens.some((existing) => existing.accessId === access.accessId)) {
        challenge.opens.push(access);
      }
      state.now = Math.max(state.now, access.openedAt);
      break;
    }
    case "device.confirmed": {
      const { tokenId, confirmation, payloadHash, acceptedAt, receipt } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge) throw new Error(`Unknown token ${tokenId} during replay`);
      const existingDevice = challenge.confirmation
        ? state.devices.get(
            deviceKey(
              challenge.claims.accountId,
              challenge.confirmation.confirmation.payload.deviceId,
            ),
          )
        : undefined;
      const existingDeviceActive =
        existingDevice !== undefined &&
        existingDevice.revokedAt === undefined &&
        existingDevice.keyId === challenge.confirmation!.confirmation.payload.deviceKeyId;
      if (!challenge.confirmation || !existingDeviceActive) {
        challenge.confirmation = { confirmation, payloadHash, acceptedAt, receipt };
      }
      rememberReceipt(state, receipt);
      state.now = Math.max(state.now, acceptedAt);
      break;
    }
    case "browser.proof_accepted": {
      const { tokenId, proof, payloadHash, verifiedAt } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge) throw new Error(`Unknown token ${tokenId} during replay`);
      if (!challenge.proof) challenge.proof = { proof, payloadHash, verifiedAt };
      state.now = Math.max(state.now, verifiedAt);
      break;
    }
    case "issuance.intent": {
      const { tokenId, request, requestHash, at } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge) throw new Error(`Unknown token ${tokenId} during replay`);
      if (!challenge.issuance) {
        challenge.issuance = {
          transactionId: request.transactionId,
          request,
          requestHash,
          requestedAt: at,
        };
      }
      state.now = Math.max(state.now, at);
      break;
    }
    case "issuance.result": {
      const { tokenId, transactionId, kind, credential, receipt, at, reason } = event.data;
      const challenge = challengeByTokenId(state, tokenId);
      if (!challenge || !challenge.issuance) {
        throw new Error(`Issuance result without intent for ${tokenId} during replay`);
      }
      if (challenge.issuance.transactionId !== transactionId) {
        throw new Error(`Issuance transaction mismatch for ${tokenId} during replay`);
      }
      challenge.issuance.result = { kind, credential, receipt, resolvedAt: at, reason };
      rememberReceipt(state, receipt);
      state.now = Math.max(state.now, at);
      break;
    }
  }
  state.eventCount += 1;
  state.lastEventHash = sha256(`${state.eventCount}\n${JSON.stringify(event)}`);
}

function challengeByTokenId(state: AuthState, tokenId: string): ChallengeView | undefined {
  const challengeId = state.tokenToChallenge.get(tokenId);
  return challengeId ? state.challenges.get(challengeId) : undefined;
}

export function findChallengeByToken(state: AuthState, tokenId: string): ChallengeView | undefined {
  return challengeByTokenId(state, tokenId);
}

export function findChallengeByTokenObject(
  state: AuthState,
  token: EmailToken,
): ChallengeView | undefined {
  return challengeByTokenId(state, token.claims.tokenId);
}

function baseStatus(
  challenge: ChallengeView,
  state: AuthState,
  now: number,
): ChallengeStatus {
  const browser = state.browsers.get(
    browserKey(challenge.claims.accountId, challenge.claims.browserId),
  );
  const key = browser?.keys[challenge.claims.browserKeyId];
  const scannerAccessCount = challenge.opens.filter(
    (access) => access.kind === "scanner" || access.kind === "prefetch",
  ).length;
  const confirmationDevice = challenge.confirmation
    ? state.devices.get(
        deviceKey(
          challenge.claims.accountId,
          challenge.confirmation.confirmation.payload.deviceId,
        ),
      )
    : undefined;
  const confirmationDeviceActive =
    confirmationDevice !== undefined &&
    confirmationDevice.revokedAt === undefined &&
    confirmationDevice.keyId === challenge.confirmation!.confirmation.payload.deviceKeyId;
  const issued = challenge.issuance?.result?.kind === "issued";
  const result = challenge.issuance?.result;
  const keyRetired = key?.retiredAt !== undefined;
  const browserRevoked = browser?.revokedAt !== undefined;
  let stateName: ChallengeState;
  let reason: string | undefined;

  if (issued) {
    stateName = "issued";
  } else if (challenge.issuance && (!result || result.kind === "unknown")) {
    stateName = "unknown";
    reason = "issuance_result_unknown";
  } else if (result?.kind === "failed") {
    stateName = "failed";
    reason = result.reason ?? "issuance_failed";
  } else if (browserRevoked) {
    stateName = "revoked";
    reason = "browser_revoked";
  } else if (now >= challenge.claims.expiresAt) {
    stateName = "expired";
    reason = "challenge_expired";
  } else if (keyRetired) {
    stateName = "awaiting_browser_proof";
    reason = "browser_key_retired";
  } else if (!challenge.confirmation || !confirmationDeviceActive) {
    stateName = "awaiting_device_confirmation";
    reason = challenge.confirmation
      ? "device_confirmation_invalidated"
      : "device_confirmation_required";
  } else {
    stateName = "awaiting_browser_proof";
    reason = "browser_proof_required";
  }

  return {
    state: stateName,
    status: stateName,
    reason,
    tokenId: challenge.tokenId,
    challengeId: challenge.challengeId,
    accountId: challenge.claims.accountId,
    email: challenge.claims.email,
    browserId: challenge.claims.browserId,
    browserSessionId: challenge.claims.browserSessionId,
    browserKeyId: challenge.claims.browserKeyId,
    purpose: challenge.claims.purpose,
    userCode: challenge.claims.userCode,
    issuedAt: challenge.claims.issuedAt,
    expiresAt: challenge.claims.expiresAt,
    emailDelivered: challenge.delivery !== undefined,
    deliveryId: challenge.delivery?.deliveryId,
    emailOpenCount: challenge.opens.length,
    scannerAccessCount,
    deviceConfirmed: challenge.confirmation !== undefined && confirmationDeviceActive,
    browserProofAccepted: challenge.proof !== undefined,
    transactionId: challenge.issuance?.transactionId,
    credentialId: result?.credential?.credentialId,
    credential: result?.credential,
    receipt: result?.receipt,
    receiptHash: result?.receipt?.hash,
    dispatchReceipt: challenge.dispatchReceipt,
    deliveryReceipt: challenge.delivery?.receipt,
    confirmationReceipt: challenge.confirmation?.receipt,
    browserRevoked,
    browserKeyRetired: keyRetired,
  };
}

export function challengeStatus(
  state: AuthState,
  challenge: ChallengeView,
  now: number,
): ChallengeStatus {
  return baseStatus(challenge, state, now);
}

/**
 * Email-link status endpoints may be hit by scanners. Report state and
 * non-secret identifiers, but never the credential or the receipt that embeds
 * it. Only a successful browser proof may claim those.
 */
export function publicChallengeStatus(status: ChallengeStatus): ChallengeStatus {
  const publicStatus: ChallengeStatus = { ...status };
  const receiptHash = status.receiptHash ?? status.receipt?.hash;
  delete publicStatus.credential;
  delete publicStatus.receipt;
  if (receiptHash !== undefined) publicStatus.receiptHash = receiptHash;
  return publicStatus;
}

export function challengeStatusByToken(
  state: AuthState,
  token: EmailToken,
  now: number,
): ChallengeStatus {
  const challenge = findChallengeByTokenObject(state, token);
  if (!challenge) {
    return notFoundStatus("unknown_token", token.claims.tokenId, token.claims.challengeId);
  }
  return publicChallengeStatus(baseStatus(challenge, state, now));
}

export function challengeStatusByTokenId(
  state: AuthState,
  tokenId: string,
  now: number,
): ChallengeStatus {
  const challenge = challengeByTokenId(state, tokenId);
  if (!challenge) return notFoundStatus("unknown_token", tokenId);
  return publicChallengeStatus(baseStatus(challenge, state, now));
}

export function notFoundStatus(
  reason: string,
  tokenId?: string,
  challengeId?: string,
): ChallengeStatus {
  return {
    state: "unknown",
    status: "unknown",
    reason,
    tokenId,
    challengeId,
    emailDelivered: false,
    emailOpenCount: 0,
    scannerAccessCount: 0,
    deviceConfirmed: false,
    browserProofAccepted: false,
  };
}

export function authorizationAudit(state: AuthState, now: number): AuthorizationAudit {
  const accountIds = new Set<string>();
  for (const browser of state.browsers.values()) accountIds.add(browser.accountId);
  for (const device of state.devices.values()) accountIds.add(device.accountId);
  for (const challenge of state.challenges.values()) accountIds.add(challenge.claims.accountId);

  const accounts: AuthorizationAudit["accounts"] = [];
  const principals: AuthorizationAudit["authorizedPrincipals"] = [];
  for (const accountId of [...accountIds].sort()) {
    const browsers = [...state.browsers.values()]
      .filter((browser) => browser.accountId === accountId)
      .sort((left, right) => left.browserId.localeCompare(right.browserId));
    const devices = [...state.devices.values()]
      .filter((device) => device.accountId === accountId)
      .sort((left, right) => left.deviceId.localeCompare(right.deviceId));
    const challenges = [...state.challenges.values()].filter(
      (challenge) => challenge.claims.accountId === accountId,
    );
    const authorizations: AuthorizationAudit["accounts"][number]["authorizations"] = [];
    const pendingChallenges: AuthorizationAudit["accounts"][number]["pendingChallenges"] = [];

    for (const challenge of challenges) {
      const status = baseStatus(challenge, state, now);
      const result = challenge.issuance?.result;
      const browser = state.browsers.get(
        browserKey(challenge.claims.accountId, challenge.claims.browserId),
      );
      const key = browser?.keys[challenge.claims.browserKeyId];
      const browserRevoked = browser?.revokedAt !== undefined;
      const keyRetired = key?.retiredAt !== undefined;
      if (result?.kind === "issued" && result.credential && result.receipt) {
        const credentialExpiresAt =
          typeof result.credential.expiresAt === "number"
            ? result.credential.expiresAt
            : Number.POSITIVE_INFINITY;
        const credentialExpired = credentialExpiresAt <= now;
        const valid = !browserRevoked && !keyRetired && !credentialExpired;
        let reason: string | undefined;
        if (browserRevoked) reason = "browser_revoked";
        else if (keyRetired) reason = "browser_key_retired";
        else if (credentialExpired) reason = "credential_expired";
        authorizations.push({
          transactionId: challenge.issuance!.transactionId,
          receiptHash: result.receipt.hash,
          credentialId: result.credential.credentialId,
          accountId,
          browserId: challenge.claims.browserId,
          browserSessionId: challenge.claims.browserSessionId,
          browserKeyId: challenge.claims.browserKeyId,
          purpose: challenge.claims.purpose,
          issuedAt: result.credential.issuedAt,
          expiresAt: result.credential.expiresAt,
          browserRevoked,
          browserKeyRetired: keyRetired,
          valid,
        });
        principals.push({
          principal: `browser:${accountId}:${challenge.claims.browserId}:${challenge.claims.browserKeyId}`,
          accountId,
          valid,
          authorizationReceiptHash: result.receipt.hash,
          reason,
        });
      } else if (status.state !== "failed") {
        pendingChallenges.push({
          tokenId: challenge.tokenId,
          challengeId: challenge.challengeId,
          state: status.state,
        });
      }
    }

    accounts.push({
      accountId,
      browsers: browsers.map((browser) => ({
        browserId: browser.browserId,
        activeKeyId: browser.activeKeyId,
        revoked: browser.revokedAt !== undefined,
        revokedAt: browser.revokedAt,
        keys: Object.values(browser.keys).map((key) => ({
          keyId: key.keyId,
          generation: key.generation,
          active: key.retiredAt === undefined && browser.revokedAt === undefined,
        })),
      })),
      devices: devices.map((device) => ({
        deviceId: device.deviceId,
        keyId: device.keyId,
        generation: device.generation,
        active: device.revokedAt === undefined,
        revokedAt: device.revokedAt,
      })),
      authorizations,
      pendingChallenges,
    });

  }
  principals.sort((left, right) => left.principal.localeCompare(right.principal));
  return deepFreeze({ generatedAt: now, accounts, authorizedPrincipals: principals });
}

export function browserForState(
  state: AuthState,
  accountId: string,
  browserId: string,
): BrowserRecord | undefined {
  return state.browsers.get(browserKey(accountId, browserId));
}

export function deviceForState(
  state: AuthState,
  accountId: string,
  deviceId: string,
): DeviceRecord | undefined {
  return state.devices.get(deviceKey(accountId, deviceId));
}
