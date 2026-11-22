import {
  cloneFrozen,
  hashDomain,
  randomId,
  randomSecret,
  deriveUserCode,
} from "./canonical.ts";
import { createEd25519HostKeyPair, keyIdForPublicKey, normalizePublicKey } from "./keys.ts";
import {
  browserProofPayloadHash,
  confirmationPayloadHash,
  createEmailToken,
  emailTokenHash,
  parseEmailToken,
  verifyBrowserProof,
  verifyDeviceConfirmation,
  verifyEmailToken,
} from "./messages.ts";
import { AsyncMutex } from "./journal.ts";
import { createJournalRecord, verifyJournalRecord } from "./journal.ts";
import { deriveTransactionId } from "./protocol-ids.ts";
import { createReceipt } from "./receipts.ts";
import {
  applyEvent,
  authorizationAudit,
  browserKey,
  challengeStatus,
  challengeStatusByToken,
  challengeStatusByTokenId,
  emptyState,
  deviceKey,
  findChallengeByToken,
  findChallengeByTokenObject,
  notFoundStatus,
  publicChallengeStatus,
} from "./state.ts";
import type {
  AuthEvent,
  AuthState,
  AuthorizationAudit,
  BeginLoginInput,
  BrowserProof,
  ChallengeStatus,
  ConfirmationResult,
  DeliveryResult,
  DeviceConfirmation,
  EmailToken,
  EmailTokenClaims,
  HostKeyPair,
  HostOptions,
  IssuanceRequest,
  IssuedCredential,
  JournalRecord,
  LoginStart,
  OpenResult,
  RedeemInput,
  RedeemResult,
  SignedReceipt,
} from "./types.ts";
import { ProtocolError } from "./types.ts";

export interface BrowserRegistration {
  accountId: string;
  browserId: string;
  keyId: string;
  publicKey: string;
  generation: number;
  idempotent: boolean;
}

export interface DeviceRegistration {
  accountId: string;
  deviceId: string;
  keyId: string;
  publicKey: string;
  generation: number;
  idempotent: boolean;
  receipt?: SignedReceipt;
}

export interface RevocationResult {
  revoked: boolean;
  alreadyRevoked: boolean;
  receipt?: SignedReceipt;
}

interface DeliveryInput {
  token?: EmailToken | string;
  tokenId?: string;
  deliveryId?: string;
  providerRef?: string;
  deliveredAt?: number;
}

interface OpenInput {
  token?: EmailToken | string;
  tokenId?: string;
  kind?: "human" | "scanner" | "prefetch" | "unknown";
  accessId?: string;
  openedAt?: number;
  userAgentHash?: string;
}

interface RedeemAction {
  kind: "issue" | "lookup";
  request: IssuanceRequest;
  transactionId: string;
}

interface RedeemOutcome {
  kind: "issued" | "unknown" | "failed";
  credential?: IssuedCredential;
  reason?: string;
}

export class CrossDeviceAuthHost {
  readonly hostKeyPair: HostKeyPair;
  readonly publicKey: string;
  readonly keyId: string;
  #store: HostOptions["store"];
  #issuance: HostOptions["issuance"];
  #clock: () => number;
  #defaultTokenTtlMs: number;
  #maxTokenTtlMs: number;
  #state: AuthState;
  #mutex = new AsyncMutex();
  #readyPromise: Promise<void> | null = null;

  constructor(options: HostOptions) {
    this.hostKeyPair = options.hostKeyPair ?? createEd25519HostKeyPair();
    this.publicKey = this.hostKeyPair.publicKeyPem;
    this.keyId = this.hostKeyPair.keyId;
    this.#store = options.store;
    this.#issuance = options.issuance;
    this.#clock = options.clock ?? (() => Date.now());
    this.#defaultTokenTtlMs = options.defaultTokenTtlMs ?? 10 * 60_000;
    this.#maxTokenTtlMs = options.maxTokenTtlMs ?? 60 * 60_000;
    this.#state = emptyState(this.#clock());
  }

  static async open(options: HostOptions): Promise<CrossDeviceAuthHost> {
    const host = new CrossDeviceAuthHost(options);
    await host.ready();
    return host;
  }

  async ready(): Promise<void> {
    if (!this.#readyPromise) {
      this.#readyPromise = this.#mutex.run(() => this.#reload());
    }
    return this.#readyPromise;
  }

  async #reload(): Promise<void> {
    const records = await this.#store.load();
    const state = emptyState(this.#clock());
    let previous: string | null = null;
    for (let index = 0; index < records.length; index += 1) {
      const record = records[index]!;
      if (!verifyJournalRecord(record, previous, index, this.publicKey)) {
        throw new ProtocolError("journal_corrupt", `Journal record ${index} failed verification`);
      }
      applyEvent(state, record.event);
      state.lastEventHash = record.hash;
      previous = record.hash;
    }
    this.#state = state;
  }

  async #persist(event: AuthEvent): Promise<void> {
    const record = createJournalRecord(
      this.#state.eventCount,
      event,
      this.#state.lastEventHash,
      this.hostKeyPair,
    );
    try {
      await this.#store.append(record);
    } catch (error) {
      await this.#reload();
      throw error;
    }
    applyEvent(this.#state, event);
    this.#state.lastEventHash = record.hash;
  }

  #makeReceipt(
    type: "email.dispatch" | "email.delivery" | "device.confirmation" | "browser.revoked" | "device.replaced" | "authorization.issued",
    payload: Record<string, unknown>,
    issuedAt: number,
  ): SignedReceipt {
    return createReceipt({
      type,
      payload,
      issuedAt,
      prevReceiptHash: this.#state.lastReceiptHash,
      signer: this.hostKeyPair,
    });
  }

  async registerBrowser(input: {
    accountId: string;
    browserId: string;
    publicKey: string;
  }): Promise<BrowserRegistration> {
    await this.ready();
    return this.#mutex.run(async () => {
      if (!input.accountId || !input.browserId) {
        throw new ProtocolError("invalid_input", "accountId and browserId are required");
      }
      const publicKey = normalizePublicKey(input.publicKey);
      const keyId = keyIdForPublicKey(publicKey);
      const existing = this.#state.browsers.get(browserKey(input.accountId, input.browserId));
      if (existing) {
        if (existing.accountId !== input.accountId) {
          throw new ProtocolError("browser_account_conflict", "Browser belongs to another account");
        }
        const key = existing.keys[keyId];
        if (key) {
          return {
            accountId: existing.accountId,
            browserId: existing.browserId,
            keyId,
            publicKey,
            generation: key.generation,
            idempotent: true,
          };
        }
        throw new ProtocolError(
          "browser_exists",
          "Browser already exists with a different key; use rotateBrowserKey",
        );
      }
      const at = this.#clock();
      await this.#persist({
        type: "browser.registered",
        data: {
          accountId: input.accountId,
          browserId: input.browserId,
          key: { keyId, publicKey, generation: 1, registeredAt: at },
          at,
        },
      });
      return {
        accountId: input.accountId,
        browserId: input.browserId,
        keyId,
        publicKey,
        generation: 1,
        idempotent: false,
      };
    });
  }

  async rotateBrowserKey(input: {
    accountId: string;
    browserId: string;
    publicKey: string;
  }): Promise<BrowserRegistration> {
    await this.ready();
    return this.#mutex.run(async () => {
      const browser = this.#state.browsers.get(browserKey(input.accountId, input.browserId));
      if (!browser || browser.accountId !== input.accountId) {
        throw new ProtocolError("unknown_browser", "Browser is not registered");
      }
      if (browser.revokedAt !== undefined) {
        throw new ProtocolError("browser_revoked", "Cannot rotate a revoked browser");
      }
      const publicKey = normalizePublicKey(input.publicKey);
      const keyId = keyIdForPublicKey(publicKey);
      const existing = browser.keys[keyId];
      if (existing && existing.retiredAt === undefined) {
        return {
          accountId: browser.accountId,
          browserId: browser.browserId,
          keyId,
          publicKey,
          generation: existing.generation,
          idempotent: true,
        };
      }
      const current = browser.keys[browser.activeKeyId]!;
      const at = this.#clock();
      await this.#persist({
        type: "browser.key_rotated",
        data: {
          accountId: input.accountId,
          browserId: input.browserId,
          key: { keyId, publicKey, generation: current.generation + 1, registeredAt: at },
          retiredKeyId: current.keyId,
          at,
        },
      });
      return {
        accountId: input.accountId,
        browserId: input.browserId,
        keyId,
        publicKey,
        generation: current.generation + 1,
        idempotent: false,
      };
    });
  }

  async revokeBrowser(input: {
    accountId: string;
    browserId: string;
  }): Promise<RevocationResult> {
    await this.ready();
    return this.#mutex.run(async () => {
      const browser = this.#state.browsers.get(browserKey(input.accountId, input.browserId));
      if (!browser || browser.accountId !== input.accountId) {
        throw new ProtocolError("unknown_browser", "Browser is not registered");
      }
      if (browser.revokedAt !== undefined) {
        return {
          revoked: true,
          alreadyRevoked: true,
          receipt: browser.revocationReceipt,
        };
      }
      const at = this.#clock();
      const receipt = this.#makeReceipt(
        "browser.revoked",
        {
          accountId: input.accountId,
          browserId: input.browserId,
          activeKeyId: browser.activeKeyId,
          revokedAt: at,
        },
        at,
      );
      await this.#persist({
        type: "browser.revoked",
        data: {
          accountId: input.accountId,
          browserId: input.browserId,
          at,
          receipt,
        },
      });
      return { revoked: true, alreadyRevoked: false, receipt };
    });
  }

  async registerDevice(input: {
    accountId: string;
    deviceId: string;
    publicKey: string;
  }): Promise<DeviceRegistration> {
    await this.ready();
    return this.#mutex.run(async () => {
      if (!input.accountId || !input.deviceId) {
        throw new ProtocolError("invalid_input", "accountId and deviceId are required");
      }
      const publicKey = normalizePublicKey(input.publicKey);
      const keyId = keyIdForPublicKey(publicKey);
      const existing = this.#state.devices.get(deviceKey(input.accountId, input.deviceId));
      if (existing) {
        if (existing.accountId !== input.accountId || existing.keyId !== keyId) {
          throw new ProtocolError(
            "device_conflict",
            "Device already exists with different account or key; use replaceDevice",
          );
        }
        if (existing.revokedAt !== undefined) {
          throw new ProtocolError("device_revoked", "Device has been revoked");
        }
        return {
          accountId: existing.accountId,
          deviceId: existing.deviceId,
          keyId: existing.keyId,
          publicKey: existing.publicKey,
          generation: existing.generation,
          idempotent: true,
        };
      }
      const at = this.#clock();
      await this.#persist({
        type: "device.registered",
        data: {
          accountId: input.accountId,
          deviceId: input.deviceId,
          keyId,
          publicKey,
          generation: 1,
          at,
        },
      });
      return {
        accountId: input.accountId,
        deviceId: input.deviceId,
        keyId,
        publicKey,
        generation: 1,
        idempotent: false,
      };
    });
  }

  async replaceDevice(input: {
    accountId: string;
    oldDeviceId: string;
    newDeviceId: string;
    publicKey: string;
  }): Promise<DeviceRegistration> {
    await this.ready();
    return this.#mutex.run(async () => {
      const oldDevice = this.#state.devices.get(deviceKey(input.accountId, input.oldDeviceId));
      if (!oldDevice || oldDevice.accountId !== input.accountId) {
        throw new ProtocolError("unknown_device", "Old device is not registered");
      }
      const publicKey = normalizePublicKey(input.publicKey);
      const keyId = keyIdForPublicKey(publicKey);
      const existingNew = this.#state.devices.get(deviceKey(input.accountId, input.newDeviceId));
      if (existingNew) {
        if (
          oldDevice.replacedBy === input.newDeviceId &&
          existingNew.keyId === keyId &&
          existingNew.accountId === input.accountId
        ) {
          return {
            accountId: existingNew.accountId,
            deviceId: existingNew.deviceId,
            keyId: existingNew.keyId,
            publicKey: existingNew.publicKey,
            generation: existingNew.generation,
            idempotent: true,
          };
        }
        throw new ProtocolError("device_conflict", "New device id is already registered");
      }
      if (oldDevice.revokedAt !== undefined) {
        throw new ProtocolError("device_not_active", "Old device is no longer active");
      }
      const at = this.#clock();
      const generation = oldDevice.generation + 1;
      const receipt = this.#makeReceipt(
        "device.replaced",
        {
          accountId: input.accountId,
          oldDeviceId: input.oldDeviceId,
          newDeviceId: input.newDeviceId,
          newKeyId: keyId,
          newPublicKey: publicKey,
          generation,
          replacedAt: at,
        },
        at,
      );
      await this.#persist({
        type: "device.replaced",
        data: {
          accountId: input.accountId,
          oldDeviceId: input.oldDeviceId,
          newDeviceId: input.newDeviceId,
          keyId,
          publicKey,
          generation,
          at,
          receipt,
        },
      });
      return {
        accountId: input.accountId,
        deviceId: input.newDeviceId,
        keyId,
        publicKey,
        generation,
        idempotent: false,
        receipt,
      };
    });
  }

  async revokeDevice(input: { accountId: string; deviceId: string }): Promise<void> {
    await this.ready();
    await this.#mutex.run(async () => {
      const device = this.#state.devices.get(deviceKey(input.accountId, input.deviceId));
      if (!device || device.accountId !== input.accountId) {
        throw new ProtocolError("unknown_device", "Device is not registered");
      }
      if (device.revokedAt !== undefined) return;
      const at = this.#clock();
      await this.#persist({
        type: "device.revoked",
        data: { accountId: input.accountId, deviceId: input.deviceId, at },
      });
    });
  }

  async beginLogin(input: BeginLoginInput): Promise<LoginStart> {
    await this.ready();
    return this.#mutex.run(async () => {
      if (!input.accountId || !input.email || !input.browserId) {
        throw new ProtocolError("invalid_input", "accountId, email and browserId are required");
      }
      const browser = this.#state.browsers.get(browserKey(input.accountId, input.browserId));
      if (!browser || browser.accountId !== input.accountId) {
        throw new ProtocolError("unknown_browser", "Initiating browser is not registered");
      }
      if (browser.revokedAt !== undefined) {
        throw new ProtocolError("browser_revoked", "Initiating browser has been revoked");
      }
      const browserKeyId = input.browserKeyId ?? browser.activeKeyId;
      const key = browser.keys[browserKeyId];
      if (!key || key.retiredAt !== undefined) {
        throw new ProtocolError("browser_key_unavailable", "Browser key is not active");
      }
      const ttlMs = input.ttlMs ?? this.#defaultTokenTtlMs;
      if (!Number.isFinite(ttlMs) || ttlMs <= 0 || ttlMs > this.#maxTokenTtlMs) {
        throw new ProtocolError("invalid_ttl", "Token lifetime is outside the permitted range");
      }
      const purpose = input.purpose ?? "sign-in";
      const now = this.#clock();
      const claims: EmailTokenClaims = {
        v: 1,
        tokenId: randomId("tok"),
        challengeId: randomId("chl"),
        accountId: input.accountId,
        email: input.email,
        browserId: input.browserId,
        browserSessionId: input.browserSessionId ?? null,
        browserKeyId,
        browserPublicKey: key.publicKey,
        challenge: randomSecret(32),
        purpose,
        userCode: input.userCode ?? deriveUserCode(input.accountId, input.email, purpose),
        issuedAt: now,
        expiresAt: now + ttlMs,
      };
      const token = createEmailToken(claims, this.hostKeyPair);
      const tokenHash = emailTokenHash(token);
      const dispatchReceipt = this.#makeReceipt(
        "email.dispatch",
        {
          tokenId: claims.tokenId,
          challengeId: claims.challengeId,
          tokenHash,
          accountId: claims.accountId,
          email: claims.email,
          browserId: claims.browserId,
          browserSessionId: claims.browserSessionId,
          browserKeyId: claims.browserKeyId,
          browserPublicKeyHash: keyIdForPublicKey(claims.browserPublicKey),
          purpose: claims.purpose,
          userCode: claims.userCode,
          issuedAt: claims.issuedAt,
          expiresAt: claims.expiresAt,
          deliveryState: "not_delivered",
        },
        now,
      );
      await this.#persist({
        type: "login.started",
        data: { claims, token, tokenHash, dispatchReceipt, at: now },
      });
      return {
        token,
        emailToken: token.encoded,
        tokenId: claims.tokenId,
        challengeId: claims.challengeId,
        accountId: claims.accountId,
        email: claims.email,
        browserId: claims.browserId,
        browserSessionId: claims.browserSessionId,
        browserKeyId: claims.browserKeyId,
        purpose: claims.purpose,
        userCode: claims.userCode,
        expiresAt: claims.expiresAt,
        dispatchReceipt,
        status: challengeStatusByToken(this.#state, token, now),
      };
    });
  }

  async recordEmailDelivered(
    inputOrToken: DeliveryInput | EmailToken | string,
    extra: Omit<DeliveryInput, "token" | "tokenId"> | string = {},
  ): Promise<DeliveryResult> {
    await this.ready();
    return this.#mutex.run(async () => {
      const normalizedExtra = typeof extra === "string" ? { deliveryId: extra } : extra;
      const input = normalizeDeliveryInput(inputOrToken, normalizedExtra);
      const resolved = this.#resolveToken(input.token, input.tokenId);
      if (!resolved.ok) {
        return {
          delivered: false,
          duplicate: false,
          reason: resolved.reason,
          status: notFoundStatus(resolved.reason, input.tokenId),
        };
      }
      const challenge = resolved.challenge;
      const existing = challenge.delivery;
      const deliveryId = input.deliveryId ?? randomId("dlv");
      if (existing) {
        const duplicate = existing.deliveryId === deliveryId;
        return {
          delivered: duplicate,
          duplicate,
          reason: duplicate ? undefined : "delivery_conflict",
          receipt: existing.receipt,
          status: challengeStatusByTokenId(
            this.#state,
            challenge.tokenId,
            this.#clock(),
          ),
        };
      }
      const deliveredAt = input.deliveredAt ?? this.#clock();
      const receipt = this.#makeReceipt(
        "email.delivery",
        {
          tokenId: challenge.tokenId,
          challengeId: challenge.challengeId,
          tokenHash: challenge.tokenHash,
          dispatchReceiptHash: challenge.dispatchReceipt.hash,
          accountId: challenge.claims.accountId,
          email: challenge.claims.email,
          deliveryId,
          providerRef: input.providerRef,
          deliveredAt,
          deliveryState: "delivered",
        },
        deliveredAt,
      );
      await this.#persist({
        type: "email.delivered",
        data: {
          tokenId: challenge.tokenId,
          deliveryId,
          deliveredAt,
          providerRef: input.providerRef,
          receipt,
        },
      });
      return {
        delivered: true,
        duplicate: false,
        receipt,
        status: challengeStatusByTokenId(this.#state, challenge.tokenId, this.#clock()),
      };
    });
  }

  async recordEmailOpened(
    inputOrToken: OpenInput | EmailToken | string,
    extra:
      | Omit<OpenInput, "token" | "tokenId">
      | "human"
      | "scanner"
      | "prefetch"
      | "unknown" = {},
  ): Promise<OpenResult> {
    await this.ready();
    return this.#mutex.run(async () => {
      const normalizedExtra = typeof extra === "string" ? { kind: extra } : extra;
      const input = normalizeOpenInput(inputOrToken, normalizedExtra);
      const resolved = this.#resolveToken(input.token, input.tokenId);
      if (!resolved.ok) {
        return {
          recorded: false,
          consumedAuthorization: false,
          duplicate: false,
          reason: resolved.reason,
          status: notFoundStatus(resolved.reason, input.tokenId),
        };
      }
      const challenge = resolved.challenge;
      const accessId = input.accessId ?? randomId("acc");
      const existing = challenge.opens.find((access) => access.accessId === accessId);
      if (existing) {
        return {
          recorded: true,
          consumedAuthorization: false,
          duplicate: true,
          status: challengeStatusByTokenId(this.#state, challenge.tokenId, this.#clock()),
        };
      }
      const openedAt = input.openedAt ?? this.#clock();
      await this.#persist({
        type: "email.opened",
        data: {
          tokenId: challenge.tokenId,
          access: {
            accessId,
            kind: input.kind ?? "unknown",
            openedAt,
            userAgentHash: input.userAgentHash,
          },
        },
      });
      return {
        recorded: true,
        consumedAuthorization: false,
        duplicate: false,
        status: challengeStatusByTokenId(this.#state, challenge.tokenId, this.#clock()),
      };
    });
  }

  async confirmOnDevice(
    input:
      | DeviceConfirmation
      | { confirmation: DeviceConfirmation; token?: EmailToken | string }
      | EmailToken
      | string,
    maybeConfirmation?: DeviceConfirmation,
  ): Promise<ConfirmationResult> {
    await this.ready();
    return this.#mutex.run(async () => {
      let confirmation: DeviceConfirmation | undefined;
      let tokenInput: EmailToken | string | undefined;
      if (maybeConfirmation !== undefined) {
        confirmation = maybeConfirmation;
        tokenInput = input as EmailToken | string;
      } else if (typeof input === "object" && input !== null && "payload" in input) {
        confirmation = input as DeviceConfirmation;
      } else if (typeof input === "object" && input !== null && "confirmation" in input) {
        confirmation = (input as { confirmation: DeviceConfirmation }).confirmation;
        tokenInput = (input as { token?: EmailToken | string }).token;
      }
      const fallback = notFoundStatus(
        "unknown_token",
        confirmation?.payload?.tokenId,
        confirmation?.payload?.challengeId,
      );
      if (!confirmation || !verifyDeviceConfirmation(confirmation)) {
        return {
          accepted: false,
          duplicate: false,
          reason: "invalid_device_signature",
          status: fallback,
        };
      }
      const challenge = findChallengeByToken(this.#state, confirmation.payload.tokenId);
      if (!challenge) {
        return {
          accepted: false,
          duplicate: false,
          reason: "unknown_token",
          status: fallback,
        };
      }
      if (!verifyEmailToken(challenge.token, this.publicKey)) {
        return {
          accepted: false,
          duplicate: false,
          reason: "invalid_token_signature",
          status: notFoundStatus(
            "invalid_token_signature",
            challenge.tokenId,
            challenge.challengeId,
          ),
        };
      }
      if (tokenInput !== undefined) {
        let supplied: EmailToken;
        try {
          supplied = parseEmailToken(tokenInput);
        } catch {
          return {
            accepted: false,
            duplicate: false,
            reason: "malformed_token",
            status: notFoundStatus("malformed_token", challenge.tokenId, challenge.challengeId),
          };
        }
        if (!verifyEmailToken(supplied, this.publicKey)) {
          return {
            accepted: false,
            duplicate: false,
            reason: "invalid_token_signature",
            status: notFoundStatus(
              "invalid_token_signature",
              challenge.tokenId,
              challenge.challengeId,
            ),
          };
        }
        if (supplied.claims.tokenId !== challenge.tokenId) {
          return {
            accepted: false,
            duplicate: false,
            reason: "token_mismatch",
            status: notFoundStatus("token_mismatch", challenge.tokenId, challenge.challengeId),
          };
        }
      }

      const status = challengeStatusByTokenId(this.#state, challenge.tokenId, this.#clock());
      const device = this.#state.devices.get(
        deviceKey(challenge.claims.accountId, confirmation.payload.deviceId),
      );
      if (!device || device.accountId !== challenge.claims.accountId) {
        return {
          accepted: false,
          duplicate: false,
          reason: "device_account_mismatch",
          status,
        };
      }
      if (device.revokedAt !== undefined) {
        return { accepted: false, duplicate: false, reason: "device_revoked", status };
      }
      if (
        device.keyId !== confirmation.payload.deviceKeyId ||
        keyIdForPublicKey(confirmation.signerPublicKey) !== device.keyId
      ) {
        return {
          accepted: false,
          duplicate: false,
          reason: "device_key_mismatch",
          status,
        };
      }
      if (
        confirmation.payload.accountId !== challenge.claims.accountId ||
        confirmation.payload.challengeId !== challenge.challengeId ||
        confirmation.payload.challenge !== challenge.claims.challenge ||
        confirmation.payload.email !== challenge.claims.email ||
        confirmation.payload.purpose !== challenge.claims.purpose ||
        confirmation.payload.expiresAt !== challenge.claims.expiresAt ||
        confirmation.payload.browserId !== challenge.claims.browserId ||
        confirmation.payload.browserSessionId !== challenge.claims.browserSessionId ||
        confirmation.payload.browserKeyId !== challenge.claims.browserKeyId
      ) {
        return {
          accepted: false,
          duplicate: false,
          reason: "confirmation_binding_mismatch",
          status,
        };
      }
      if (
        confirmation.payload.selectedBrowserId !== challenge.claims.browserId ||
        confirmation.payload.selectedBrowserSessionId !== challenge.claims.browserSessionId
      ) {
        return {
          accepted: false,
          duplicate: false,
          reason: "selected_browser_mismatch",
          status,
        };
      }
      if (status.state === "expired") {
        return { accepted: false, duplicate: false, reason: "challenge_expired", status };
      }
      if (status.state === "revoked") {
        return { accepted: false, duplicate: false, reason: "browser_revoked", status };
      }
      if (
        status.state === "issued" ||
        status.state === "unknown" ||
        status.state === "failed"
      ) {
        return { accepted: false, duplicate: false, reason: "challenge_not_pending", status };
      }
      if (status.browserKeyRetired) {
        return { accepted: false, duplicate: false, reason: "browser_key_retired", status };
      }

      const payloadHash = confirmationPayloadHash(confirmation);
      if (challenge.confirmation) {
        const existingDevice = this.#state.devices.get(
          deviceKey(
            challenge.claims.accountId,
            challenge.confirmation.confirmation.payload.deviceId,
          ),
        );
        const existingConfirmationActive =
          existingDevice !== undefined &&
          existingDevice.revokedAt === undefined &&
          existingDevice.keyId === challenge.confirmation.confirmation.payload.deviceKeyId;
        if (existingConfirmationActive && challenge.confirmation.payloadHash === payloadHash) {
          return {
            accepted: true,
            duplicate: true,
            receipt: challenge.confirmation.receipt,
            status,
          };
        }
        if (existingConfirmationActive) {
          return {
            accepted: false,
            duplicate: false,
            reason: "confirmation_conflict",
            status,
          };
        }
      }
      const acceptedAt = this.#clock();
      const receipt = this.#makeReceipt(
        "device.confirmation",
        {
          tokenId: challenge.tokenId,
          challengeId: challenge.challengeId,
          tokenHash: challenge.tokenHash,
          accountId: challenge.claims.accountId,
          browserId: challenge.claims.browserId,
          browserSessionId: challenge.claims.browserSessionId,
          browserKeyId: challenge.claims.browserKeyId,
          deviceId: device.deviceId,
          deviceKeyId: device.keyId,
          devicePublicKey: device.publicKey,
          selectedBrowserId: confirmation.payload.selectedBrowserId,
          selectedBrowserSessionId: confirmation.payload.selectedBrowserSessionId,
          confirmation: confirmation,
          confirmationHash: payloadHash,
          confirmedAt: confirmation.payload.confirmedAt,
          acceptedAt,
        },
        acceptedAt,
      );
      await this.#persist({
        type: "device.confirmed",
        data: {
          tokenId: challenge.tokenId,
          confirmation,
          payloadHash,
          acceptedAt,
          receipt,
        },
      });
      return {
        accepted: true,
        duplicate: false,
        receipt,
        status: challengeStatusByTokenId(this.#state, challenge.tokenId, this.#clock()),
      };
    });
  }

  async getStatus(
    input?: EmailToken | string | { token?: EmailToken | string; tokenId?: string; challengeId?: string },
  ): Promise<ChallengeStatus> {
    await this.ready();
    const now = this.#clock();
    if (input === undefined) return notFoundStatus("missing_token");
    if (
      typeof input === "object" &&
      input !== null &&
      "claims" in input &&
      "signature" in input
    ) {
      const token = input as EmailToken;
      if (!verifyEmailToken(token, this.publicKey)) {
        return notFoundStatus("invalid_token_signature", token.claims.tokenId, token.claims.challengeId);
      }
      const challenge = findChallengeByTokenObject(this.#state, token);
      if (!challenge) {
        return notFoundStatus("unknown_token", token.claims.tokenId, token.claims.challengeId);
      }
      return publicChallengeStatus(challengeStatus(this.#state, challenge, now));
    }
    if (typeof input === "string") {
      let token: EmailToken | null = null;
      if (input.includes(".")) {
        try {
          token = parseEmailToken(input);
        } catch {
          token = null;
        }
      }
      if (token) {
        if (!verifyEmailToken(token, this.publicKey)) {
          return notFoundStatus(
            "invalid_token_signature",
            token.claims.tokenId,
            token.claims.challengeId,
          );
        }
        return challengeStatusByToken(this.#state, token, now);
      }
      const challenge = this.#state.challenges.get(input);
      if (challenge) {
        return publicChallengeStatus(challengeStatus(this.#state, challenge, now));
      }
      return challengeStatusByTokenId(this.#state, input, now);
    }
    if (typeof input === "object" && input !== null) {
      const query = input as {
        token?: EmailToken | string;
        tokenId?: string;
        challengeId?: string;
      };
      if (query.token !== undefined) {
        let token: EmailToken;
        try {
          token = parseEmailToken(query.token);
        } catch {
          return notFoundStatus("malformed_token", query.tokenId, query.challengeId);
        }
        if (!verifyEmailToken(token, this.publicKey)) {
          return notFoundStatus(
            "invalid_token_signature",
            token.claims.tokenId,
            token.claims.challengeId,
          );
        }
        if (query.tokenId && token.claims.tokenId !== query.tokenId) {
          return notFoundStatus("token_mismatch", query.tokenId, query.challengeId);
        }
        if (query.challengeId && token.claims.challengeId !== query.challengeId) {
          return notFoundStatus("challenge_mismatch", token.claims.tokenId, query.challengeId);
        }
        return challengeStatusByToken(this.#state, token, now);
      }
      if (query.challengeId) {
        const challenge = this.#state.challenges.get(query.challengeId);
        if (!challenge) {
          return notFoundStatus("unknown_challenge", query.tokenId, query.challengeId);
        }
        if (query.tokenId && challenge.tokenId !== query.tokenId) {
          return notFoundStatus("token_mismatch", query.tokenId, query.challengeId);
        }
        return publicChallengeStatus(challengeStatus(this.#state, challenge, now));
      }
      if (query.tokenId) return challengeStatusByTokenId(this.#state, query.tokenId, now);
    }
    return notFoundStatus("invalid_status_query");
  }

  async listChallenges(filter: {
    accountId?: string;
    userCode?: string;
    browserId?: string;
  } = {}): Promise<ChallengeStatus[]> {
    await this.ready();
    const now = this.#clock();
    return [...this.#state.challenges.values()]
      .filter((challenge) => {
        if (filter.accountId && challenge.claims.accountId !== filter.accountId) return false;
        if (filter.userCode && challenge.claims.userCode !== filter.userCode) return false;
        if (filter.browserId && challenge.claims.browserId !== filter.browserId) return false;
        return true;
      })
      .map((challenge) =>
        publicChallengeStatus(challengeStatus(this.#state, challenge, now)),
      )
      .sort((left, right) =>
        (left.issuedAt ?? 0) - (right.issuedAt ?? 0) ||
        (left.tokenId ?? "").localeCompare(right.tokenId ?? ""),
      );
  }

  async redeem(
    input: RedeemInput | BrowserProof | EmailToken | string,
    maybeProof?: BrowserProof,
  ): Promise<RedeemResult> {
    await this.ready();
    let proof: BrowserProof;
    let tokenInput: EmailToken | string | undefined;
    let requestedTokenId: string | undefined;
    if (maybeProof !== undefined) {
      proof = maybeProof;
      tokenInput = input as EmailToken | string;
    } else if (!input || typeof input !== "object") {
      return this.#redeemResult(notFoundStatus("missing_browser_proof"), false, false);
    } else if (
      typeof input === "object" &&
      input !== null &&
      "payload" in input &&
      "signature" in input
    ) {
      proof = input as BrowserProof;
    } else {
      proof = (input as RedeemInput).proof;
      tokenInput = (input as RedeemInput).token;
      requestedTokenId = (input as RedeemInput).tokenId;
    }
    if (!proof || typeof proof !== "object" || !proof.payload) {
      return this.#redeemResult(notFoundStatus("missing_browser_proof"), false, false);
    }
    if (requestedTokenId !== undefined && proof.payload.tokenId !== requestedTokenId) {
      return this.#redeemResult(
        notFoundStatus("token_mismatch", requestedTokenId, proof.payload.challengeId),
        false,
        false,
      );
    }

    let suppliedToken: EmailToken | null = null;
    if (tokenInput !== undefined) {
      try {
        suppliedToken = parseEmailToken(tokenInput);
      } catch {
        return this.#redeemResult(
          notFoundStatus("malformed_token", proof.payload.tokenId, proof.payload.challengeId),
          false,
          false,
        );
      }
      if (!verifyEmailToken(suppliedToken, this.publicKey)) {
        return this.#redeemResult(
          notFoundStatus(
            "invalid_token_signature",
            proof.payload.tokenId,
            proof.payload.challengeId,
          ),
          false,
          false,
        );
      }
      if (suppliedToken.claims.tokenId !== proof.payload.tokenId) {
        return this.#redeemResult(
          notFoundStatus("token_mismatch", proof.payload.tokenId, proof.payload.challengeId),
          false,
          false,
        );
      }
    }

    const first = await this.#mutex.run(async () => this.#prepareRedeem(proof));
    if (first.kind === "result") return first.result;
    const action = first.action;

    let outcome: RedeemOutcome;
    try {
      if (action.kind === "issue") {
        const credential = await this.#issuance.issue(action.request);
        outcome = this.#validateCredential(credential, action.request);
      } else {
        const lookup = await this.#issuance.lookup(action.transactionId);
        if (lookup.status === "issued") {
          outcome = this.#validateCredential(lookup.credential, action.request);
        } else if (lookup.status === "failed") {
          outcome = { kind: "failed", reason: lookup.reason ?? "issuance_failed" };
        } else if (lookup.status === "pending") {
          outcome = { kind: "unknown", reason: "issuance_pending" };
        } else {
          outcome = { kind: "unknown", reason: "issuance_not_found_do_not_reissue" };
        }
      }
    } catch (error) {
      outcome = {
        kind: "unknown",
        reason:
          action.kind === "issue"
            ? "issuance_response_lost"
            : "issuance_lookup_failed",
      };
    }

    return this.#mutex.run(async () => this.#finishRedeem(action, outcome));
  }

  async #prepareRedeem(
    proof: BrowserProof,
  ): Promise<{ kind: "result"; result: RedeemResult } | { kind: "action"; action: RedeemAction }> {
    const now = this.#clock();
    const challenge = findChallengeByToken(this.#state, proof.payload.tokenId);
    if (!challenge) {
      return {
        kind: "result",
        result: this.#redeemResult(
          notFoundStatus("unknown_token", proof.payload.tokenId, proof.payload.challengeId),
          false,
          false,
        ),
      };
    }
    const status = challengeStatus(this.#state, challenge, now);
    if (status.state === "revoked") {
      const pendingUnknown =
        challenge.issuance !== undefined &&
        (!challenge.issuance.result || challenge.issuance.result.kind === "unknown");
      if (!pendingUnknown) {
        return {
          kind: "result",
          result: this.#redeemResult(status, false, false),
        };
      }
    }
    if (status.state === "expired" || status.state === "failed") {
      return {
        kind: "result",
        result: this.#redeemResult(status, false, false),
      };
    }
    if (status.state === "awaiting_device_confirmation") {
      return {
        kind: "result",
        result: this.#redeemResult(status, false, false),
      };
    }
    if (!this.#proofMatches(proof, challenge.claims)) {
      return {
        kind: "result",
        result: this.#redeemResult(
          {
            ...publicChallengeStatus(status),
            state: "awaiting_browser_proof",
            status: "awaiting_browser_proof",
            reason: "invalid_browser_proof",
          },
          false,
          false,
        ),
      };
    }
    if (status.state === "issued") {
      return {
        kind: "result",
        result: this.#redeemResult(status, true, false),
      };
    }
    if (!challenge.confirmation) {
      return {
        kind: "result",
        result: this.#redeemResult(status, false, false),
      };
    }
    if (status.browserKeyRetired && !challenge.issuance) {
      return {
        kind: "result",
        result: this.#redeemResult(status, false, false),
      };
    }
    if (now >= challenge.claims.expiresAt && !challenge.issuance) {
      const expired = challengeStatus(this.#state, challenge, now);
      return {
        kind: "result",
        result: this.#redeemResult(expired, false, false),
      };
    }

    if (!challenge.proof) {
      await this.#persist({
        type: "browser.proof_accepted",
        data: {
          tokenId: challenge.tokenId,
          proof,
          payloadHash: browserProofPayloadHash(proof),
          verifiedAt: now,
        },
      });
    }
    const current = findChallengeByToken(this.#state, challenge.tokenId)!;
    if (current.issuance) {
      const existingResult = current.issuance.result;
      if (existingResult?.kind === "issued" && existingResult.credential) {
        return {
          kind: "result",
          result: this.#redeemResult(
            challengeStatus(this.#state, current, now),
            true,
            false,
          ),
        };
      }
      return {
        kind: "action",
        action: {
          kind: "lookup",
          request: current.issuance.request,
          transactionId: current.issuance.transactionId,
        },
      };
    }
    if (!current.confirmation || !current.proof) {
      return {
        kind: "result",
        result: this.#redeemResult(
          challengeStatus(this.#state, current, now),
          false,
          false,
        ),
      };
    }
    const request: IssuanceRequest = {
      transactionId: deriveTransactionId(current.claims),
      accountId: current.claims.accountId,
      challengeId: current.claims.challengeId,
      tokenId: current.claims.tokenId,
      browserId: current.claims.browserId,
      browserSessionId: current.claims.browserSessionId,
      browserKeyId: current.claims.browserKeyId,
      purpose: current.claims.purpose,
      expiresAt: current.claims.expiresAt,
      authorizedAt: now,
      confirmationHash: current.confirmation.payloadHash,
      proofHash: current.proof.payloadHash,
    };
    await this.#persist({
      type: "issuance.intent",
      data: {
        tokenId: current.tokenId,
        request,
        requestHash: hashDomain("ddd-cross-device/issuance-request/v1", request),
        at: now,
      },
    });
    return {
      kind: "action",
      action: { kind: "issue", request, transactionId: request.transactionId },
    };
  }

  #validateCredential(credential: IssuedCredential, request: IssuanceRequest): RedeemOutcome {
    if (
      !credential ||
      typeof credential.credentialId !== "string" ||
      credential.transactionId !== request.transactionId ||
      credential.accountId !== request.accountId ||
      credential.browserId !== request.browserId ||
      credential.browserSessionId !== request.browserSessionId ||
      credential.browserKeyId !== request.browserKeyId ||
      credential.purpose !== request.purpose ||
      typeof credential.issuedAt !== "number" ||
      typeof credential.expiresAt !== "number"
    ) {
      return { kind: "unknown", reason: "issuance_response_binding_mismatch" };
    }
    return { kind: "issued", credential: cloneFrozen(credential) };
  }

  async #finishRedeem(action: RedeemAction, outcome: RedeemOutcome): Promise<RedeemResult> {
    const challenge = findChallengeByToken(this.#state, action.request.tokenId);
    if (!challenge || !challenge.issuance) {
      return this.#redeemResult(notFoundStatus("unknown_token", action.request.tokenId), false, false);
    }
    const now = this.#clock();
    if (challenge.issuance.result?.kind === "issued" && challenge.issuance.result.credential) {
      return this.#redeemResult(
        challengeStatus(this.#state, challenge, now),
        true,
        false,
      );
    }
    if (outcome.kind === "issued" && outcome.credential) {
      if (!challenge.confirmation || !challenge.proof) {
        return this.#redeemResult(
          challengeStatus(this.#state, challenge, now),
          false,
          false,
        );
      }
      const credential = outcome.credential;
      const credentialHash = hashDomain("ddd-cross-device/issued-credential/v1", credential);
      const receipt = this.#makeReceipt(
        "authorization.issued",
        {
          transactionId: action.transactionId,
          accountId: challenge.claims.accountId,
          challengeId: challenge.claims.challengeId,
          tokenId: challenge.claims.tokenId,
          browserId: challenge.claims.browserId,
          browserSessionId: challenge.claims.browserSessionId,
          browserKeyId: challenge.claims.browserKeyId,
          purpose: challenge.claims.purpose,
          expiresAt: challenge.claims.expiresAt,
          authorizedAt: action.request.authorizedAt,
          token: challenge.token,
          tokenHash: challenge.tokenHash,
          confirmation: challenge.confirmation.confirmation,
          confirmationHash: challenge.confirmation.payloadHash,
          confirmationReceiptHash: challenge.confirmation.receipt.hash,
          browserProof: challenge.proof.proof,
          proofHash: challenge.proof.payloadHash,
          credential,
          credentialHash,
          issuedAt: now,
        },
        now,
      );
      await this.#persist({
        type: "issuance.result",
        data: {
          tokenId: challenge.tokenId,
          transactionId: action.transactionId,
          kind: "issued",
          credential,
          receipt,
          at: now,
        },
      });
      const current = findChallengeByToken(this.#state, challenge.tokenId)!;
      return this.#redeemResult(
        challengeStatus(this.#state, current, now),
        true,
        true,
      );
    }
    if (outcome.kind === "failed") {
      await this.#persist({
        type: "issuance.result",
        data: {
          tokenId: challenge.tokenId,
          transactionId: action.transactionId,
          kind: "failed",
          reason: outcome.reason ?? "issuance_failed",
          at: now,
        },
      });
    } else if (!challenge.issuance.result || challenge.issuance.result.kind !== "unknown") {
      await this.#persist({
        type: "issuance.result",
        data: {
          tokenId: challenge.tokenId,
          transactionId: action.transactionId,
          kind: "unknown",
          reason: outcome.reason ?? "issuance_result_unknown",
          at: now,
        },
      });
    }
    const current = findChallengeByToken(this.#state, challenge.tokenId)!;
    return this.#redeemResult(
      challengeStatus(this.#state, current, now),
      false,
      false,
    );
  }

  #proofMatches(proof: BrowserProof, claims: EmailTokenClaims): boolean {
    if (!verifyBrowserProof(proof, claims.browserPublicKey)) return false;
    return (
      proof.payload.tokenId === claims.tokenId &&
      proof.payload.challengeId === claims.challengeId &&
      proof.payload.challenge === claims.challenge &&
      proof.payload.accountId === claims.accountId &&
      proof.payload.browserId === claims.browserId &&
      proof.payload.browserSessionId === claims.browserSessionId &&
      proof.payload.browserKeyId === claims.browserKeyId &&
      proof.payload.purpose === claims.purpose
    );
  }

  #redeemResult(status: ChallengeStatus, redeemed: boolean, newlyIssued: boolean): RedeemResult {
    return { ...status, redeemed, newlyIssued };
  }

  #resolveToken(
    tokenInput: EmailToken | string | undefined,
    tokenId: string | undefined,
  ):
    | { ok: true; challenge: NonNullable<ReturnType<typeof findChallengeByToken>> }
    | { ok: false; reason: string } {
    let token: EmailToken | null = null;
    if (tokenInput !== undefined) {
      try {
        token = parseEmailToken(tokenInput);
      } catch {
        return { ok: false, reason: "malformed_token" };
      }
      if (!verifyEmailToken(token, this.publicKey)) {
        return { ok: false, reason: "invalid_token_signature" };
      }
      if (tokenId !== undefined && token.claims.tokenId !== tokenId) {
        return { ok: false, reason: "token_mismatch" };
      }
      const challenge = findChallengeByTokenObject(this.#state, token);
      if (!challenge) return { ok: false, reason: "unknown_token" };
      return { ok: true, challenge };
    }
    if (!tokenId) return { ok: false, reason: "missing_token" };
    const challenge = findChallengeByToken(this.#state, tokenId);
    if (!challenge) return { ok: false, reason: "unknown_token" };
    return { ok: true, challenge };
  }

  startLogin(input: BeginLoginInput): Promise<LoginStart> {
    return this.beginLogin(input);
  }

  initiateLogin(input: BeginLoginInput): Promise<LoginStart> {
    return this.beginLogin(input);
  }

  confirm(
    input:
      | DeviceConfirmation
      | { confirmation: DeviceConfirmation; token?: EmailToken | string }
      | EmailToken
      | string,
    maybeConfirmation?: DeviceConfirmation,
  ): Promise<ConfirmationResult> {
    return this.confirmOnDevice(input, maybeConfirmation);
  }

  submitDeviceConfirmation(
    input:
      | DeviceConfirmation
      | { confirmation: DeviceConfirmation; token?: EmailToken | string }
      | EmailToken
      | string,
    maybeConfirmation?: DeviceConfirmation,
  ): Promise<ConfirmationResult> {
    return this.confirmOnDevice(input, maybeConfirmation);
  }

  claim(
    input: RedeemInput | BrowserProof | EmailToken | string,
    maybeProof?: BrowserProof,
  ): Promise<RedeemResult> {
    return this.redeem(input, maybeProof);
  }

  redeemLogin(
    input: RedeemInput | BrowserProof | EmailToken | string,
    maybeProof?: BrowserProof,
  ): Promise<RedeemResult> {
    return this.redeem(input, maybeProof);
  }

  openEmailToken(
    token: EmailToken | string,
    kind: "human" | "scanner" | "prefetch" | "unknown" = "human",
    accessId?: string,
  ): Promise<OpenResult> {
    return this.recordEmailOpened({ token, kind, accessId });
  }

  prefetchEmail(token: EmailToken | string, accessId?: string): Promise<OpenResult> {
    return this.recordEmailOpened({ token, kind: "prefetch", accessId });
  }

  deliverEmail(
    token: EmailToken | string,
    deliveryId?: string,
    providerRef?: string,
  ): Promise<DeliveryResult> {
    return this.recordEmailDelivered({ token, deliveryId, providerRef });
  }

  getChallengeStatus(input: EmailToken | string | { token?: EmailToken | string; tokenId?: string; challengeId?: string }): Promise<ChallengeStatus> {
    return this.getStatus(input);
  }

  getConfirmationStatus(confirmation: DeviceConfirmation): Promise<ChallengeStatus> {
    return this.getStatus({
      tokenId: confirmation.payload.tokenId,
      challengeId: confirmation.payload.challengeId,
    });
  }

  async journalRecords(): Promise<JournalRecord[]> {
    await this.ready();
    return this.#store.load();
  }

  authorizationAudit(): AuthorizationAudit {
    return authorizationAudit(this.#state, this.#clock());
  }

  audit(): AuthorizationAudit {
    return this.authorizationAudit();
  }

  getAuthorizationAudit(): AuthorizationAudit {
    return this.authorizationAudit();
  }

  receipts(): SignedReceipt[] {
    return [...this.#state.receiptsByHash.values()];
  }

  getReceipts(): SignedReceipt[] {
    return this.receipts();
  }
}

function normalizeDeliveryInput(
  inputOrToken: DeliveryInput | EmailToken | string,
  extra: Omit<DeliveryInput, "token" | "tokenId">,
): DeliveryInput {
  if (typeof inputOrToken === "string") {
    return { token: inputOrToken, ...extra };
  }
  if (typeof inputOrToken === "object" && inputOrToken !== null && "payload" in inputOrToken) {
    return { token: inputOrToken as unknown as EmailToken, ...extra };
  }
  if (typeof inputOrToken === "object" && inputOrToken !== null && "claims" in inputOrToken) {
    return { token: inputOrToken as EmailToken, ...extra };
  }
  return { ...(inputOrToken as DeliveryInput), ...extra };
}

function normalizeOpenInput(
  inputOrToken: OpenInput | EmailToken | string,
  extra: Omit<OpenInput, "token" | "tokenId">,
): OpenInput {
  if (typeof inputOrToken === "string") {
    return { token: inputOrToken, ...extra };
  }
  if (typeof inputOrToken === "object" && inputOrToken !== null && "claims" in inputOrToken) {
    return { token: inputOrToken as EmailToken, ...extra };
  }
  return { ...(inputOrToken as OpenInput), ...extra };
}
