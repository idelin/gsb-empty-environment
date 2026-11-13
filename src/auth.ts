import type {
  AuthEpochAuthority,
  AuthEpochEvent,
  AuthEpochState,
  MaybePromise,
} from "./types.ts";

const AUTH_EPOCH_DOMAIN = "bfcache-auth-epoch";
const AUTH_EPOCH_VERSION = 1;

const encoder = new TextEncoder();

function subtleCrypto(): any {
  const subtle = (globalThis as any).crypto?.subtle;
  if (!subtle) {
    throw new Error(
      "WebCrypto subtle is unavailable; inject an AuthEpochAuthority for this runtime",
    );
  }
  return subtle;
}

export function bytesToBase64(bytes: Uint8Array): string {
  const bufferCtor = (globalThis as any).Buffer;
  if (bufferCtor) {
    return bufferCtor.from(bytes).toString("base64");
  }
  let binary = "";
  for (let index = 0; index < bytes.length; index += 1) {
    binary += String.fromCharCode(bytes[index]);
  }
  return (globalThis as any).btoa(binary);
}

export function base64ToBytes(value: string): Uint8Array {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized + "=".repeat((4 - (normalized.length % 4)) % 4);
  const bufferCtor = (globalThis as any).Buffer;
  if (bufferCtor) {
    return new Uint8Array(bufferCtor.from(padded, "base64"));
  }
  const binary = (globalThis as any).atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

/**
 * The exact byte sequence signed by the account authentication service.
 *
 * Only the four fields named by the protocol are signed.  `reason`, `alg` and
 * `keyID` are diagnostic/routing metadata and cannot affect authorization.
 */
export function canonicalAuthEpochPayload(event: {
  accountID: string;
  authSessionID: string;
  epoch: number;
  state: AuthEpochState;
}): Uint8Array {
  return encoder.encode(
    JSON.stringify({
      domain: AUTH_EPOCH_DOMAIN,
      version: AUTH_EPOCH_VERSION,
      accountID: event.accountID,
      authSessionID: event.authSessionID,
      epoch: event.epoch,
      state: event.state,
    }),
  );
}

export function isWellFormedAuthEpochEvent(event: unknown): boolean {
  if (!event || typeof event !== "object") {
    return false;
  }
  const candidate = event as Partial<AuthEpochEvent>;
  return (
    (candidate.type === undefined || candidate.type === "AuthEpoch") &&
    typeof candidate.accountID === "string" &&
    candidate.accountID.length > 0 &&
    typeof candidate.authSessionID === "string" &&
    candidate.authSessionID.length > 0 &&
    Number.isSafeInteger(candidate.epoch) &&
    (candidate.epoch as number) >= 0 &&
    (candidate.state === "Active" || candidate.state === "Revoked") &&
    (candidate.alg === undefined || candidate.alg === "Ed25519") &&
    (candidate.keyID === undefined ||
      (typeof candidate.keyID === "string" && candidate.keyID.length > 0)) &&
    typeof candidate.signature === "string" &&
    candidate.signature.length > 0
  );
}

async function importPublicKey(
  key: Uint8Array | string,
  subtle: any,
): Promise<any> {
  let bytes: Uint8Array;
  if (typeof key === "string") {
    bytes = base64ToBytes(key);
  } else {
    bytes = key;
  }
  const isRaw = bytes.byteLength === 32;
  return subtle.importKey(
    isRaw ? "raw" : "spki",
    bytes,
    { name: "Ed25519" },
    true,
    ["verify"],
  );
}

/**
 * Verify one Ed25519 auth-epoch event against a public key.
 *
 * `publicKey` may be a raw 32-byte key, SPKI bytes, or base64 of either.
 */
export async function verifyAuthEpochSignature(
  event: AuthEpochEvent,
  publicKey: Uint8Array | string,
  options: { subtle?: any } = {},
): Promise<boolean> {
  if (!isWellFormedAuthEpochEvent(event)) {
    return false;
  }
  const subtle = options.subtle ?? subtleCrypto();
  try {
    const key = await importPublicKey(publicKey, subtle);
    return await subtle.verify(
      { name: "Ed25519" },
      key,
      base64ToBytes(event.signature),
      canonicalAuthEpochPayload(event),
    );
  } catch {
    return false;
  }
}

export function createEd25519Verifier(
  publicKey: Uint8Array | string,
  options: { subtle?: any } = {},
): (event: AuthEpochEvent) => Promise<boolean> {
  return (event: AuthEpochEvent) =>
    verifyAuthEpochSignature(event, publicKey, options);
}

export interface AccountAuthServiceOptions {
  readonly keyID?: string;
  /** SPKI bytes/base64.  When omitted a key pair is generated. */
  readonly publicKey?: Uint8Array | string;
  /** PKCS#8 bytes.  When omitted a key pair is generated. */
  readonly privateKey?: Uint8Array;
  readonly subtle?: any;
}

export interface IssueActiveOptions {
  readonly accountID: string;
  readonly authSessionID: string;
  /** Defaults to previous epoch + 1, starting at 1. */
  readonly epoch?: number;
  readonly reason?: string;
}

export interface RevokeOptions {
  readonly accountID?: string;
  readonly authSessionID: string;
  readonly reason?: string;
}

/**
 * A small reference implementation of the account authentication service.
 *
 * It owns the Ed25519 private key, increments the epoch on every revocation,
 * and retains only the newest signed event per auth session.  Applications can
 * replace it with a remote implementation of `AuthEpochAuthority`.
 */
export class AccountAuthService implements AuthEpochAuthority {
  static async create(
    options: AccountAuthServiceOptions = {},
  ): Promise<AccountAuthService> {
    const subtle = options.subtle ?? subtleCrypto();
    let publicKeySpki: Uint8Array;
    let privateKey: any;

    if (
      (options.publicKey === undefined) !==
      (options.privateKey === undefined)
    ) {
      throw new Error(
        "publicKey and privateKey must be provided together",
      );
    }

    if (options.publicKey !== undefined && options.privateKey !== undefined) {
      publicKeySpki =
        typeof options.publicKey === "string"
          ? base64ToBytes(options.publicKey)
          : options.publicKey;
      privateKey = await subtle.importKey(
        "pkcs8",
        options.privateKey,
        { name: "Ed25519" },
        false,
        ["sign"],
      );
    } else {
      const pair = await subtle.generateKey(
        { name: "Ed25519" },
        true,
        ["sign", "verify"],
      );
      privateKey = pair.privateKey;
      publicKeySpki = new Uint8Array(
        await subtle.exportKey("spki", pair.publicKey),
      );
    }

    const verify = createEd25519Verifier(publicKeySpki, { subtle });
    return new AccountAuthService(
      privateKey,
      publicKeySpki,
      options.keyID ?? "account-auth-ed25519-1",
      subtle,
      verify,
    );
  }

  readonly #privateKey: any;
  readonly #publicKeySpki: Uint8Array;
  readonly #keyID: string;
  readonly #subtle: any;
  readonly #verify: (event: AuthEpochEvent) => Promise<boolean>;
  readonly #latest = new Map<string, AuthEpochEvent>();
  readonly #subscribers = new Set<(event: AuthEpochEvent) => void>();

  private constructor(
    privateKey: any,
    publicKeySpki: Uint8Array,
    keyID: string,
    subtle: any,
    verify: (event: AuthEpochEvent) => Promise<boolean>,
  ) {
    this.#privateKey = privateKey;
    this.#publicKeySpki = publicKeySpki;
    this.#keyID = keyID;
    this.#subtle = subtle;
    this.#verify = verify;
  }

  get keyID(): string {
    return this.#keyID;
  }

  get publicKey(): Uint8Array {
    return this.#publicKeySpki.slice();
  }

  get publicKeyBase64(): string {
    return bytesToBase64(this.#publicKeySpki);
  }

  /** Subscribe to newly issued events.  This is the push channel to a host. */
  subscribe(listener: (event: AuthEpochEvent) => void): () => void {
    this.#subscribers.add(listener);
    return () => {
      this.#subscribers.delete(listener);
    };
  }

  /** Push every newly issued event into a host. */
  bind(host: {
    enqueueAuthEpoch?(event: AuthEpochEvent): unknown;
    receiveAuthEpoch(event: AuthEpochEvent): unknown;
  }): () => void {
    return this.subscribe((event) => {
      if (host.enqueueAuthEpoch) {
        void host.enqueueAuthEpoch(event);
      } else {
        void host.receiveAuthEpoch(event);
      }
    });
  }

  peek(authSessionID: string): AuthEpochEvent | undefined {
    return this.#latest.get(authSessionID);
  }

  readCurrent(
    authSessionID: string,
  ): MaybePromise<AuthEpochEvent | undefined> {
    return this.peek(authSessionID);
  }

  verify(event: AuthEpochEvent): Promise<boolean> {
    return this.#verify(event);
  }

  async issueActive(options: IssueActiveOptions): Promise<AuthEpochEvent> {
    const previous = this.#latest.get(options.authSessionID);
    if (previous?.state === "Revoked") {
      throw new Error(
        `auth session ${options.authSessionID} is revoked; create a new session instead`,
      );
    }
    const epoch = options.epoch ?? (previous?.epoch ?? 0) + 1;
    if (!Number.isSafeInteger(epoch) || epoch < 1) {
      throw new Error("epoch must be a positive safe integer");
    }
    if (previous && epoch <= previous.epoch) {
      throw new Error(
        `epoch must advance for auth session ${options.authSessionID}`,
      );
    }
    return this.#issue(
      options.accountID,
      options.authSessionID,
      epoch,
      "Active",
      options.reason,
    );
  }

  /** Logout, password reset or risk escalation: increment epoch and Revoke. */
  async revoke(options: RevokeOptions): Promise<AuthEpochEvent> {
    const previous = this.#latest.get(options.authSessionID);
    const accountID = options.accountID ?? previous?.accountID;
    if (!accountID) {
      throw new Error(
        `accountID is required for unknown auth session ${options.authSessionID}`,
      );
    }
    return this.#issue(
      accountID,
      options.authSessionID,
      (previous?.epoch ?? 0) + 1,
      "Revoked",
      options.reason,
    );
  }

  async raiseRisk(options: RevokeOptions): Promise<AuthEpochEvent> {
    return this.revoke({ ...options, reason: options.reason ?? "risk-escalation" });
  }

  async resetPassword(accountID: string): Promise<AuthEpochEvent[]> {
    const sessions = [...this.#latest.values()]
      .filter(
        (event) =>
          event.accountID === accountID && event.state === "Active",
      )
      .map((event) => event.authSessionID);
    const events: AuthEpochEvent[] = [];
    for (const authSessionID of sessions) {
      events.push(
        await this.revoke({
          accountID,
          authSessionID,
          reason: "password-reset",
        }),
      );
    }
    return events;
  }

  async #issue(
    accountID: string,
    authSessionID: string,
    epoch: number,
    state: AuthEpochState,
    reason?: string,
  ): Promise<AuthEpochEvent> {
    const signedFields = { accountID, authSessionID, epoch, state };
    const signature = await this.#subtle.sign(
      { name: "Ed25519" },
      this.#privateKey,
      canonicalAuthEpochPayload(signedFields),
    );
    const event: AuthEpochEvent = Object.freeze({
      type: "AuthEpoch",
      ...signedFields,
      alg: "Ed25519",
      keyID: this.#keyID,
      signature: bytesToBase64(new Uint8Array(signature)),
      ...(reason ? { reason } : {}),
    });
    this.#latest.set(authSessionID, event);
    for (const listener of [...this.#subscribers]) {
      try {
        listener(event);
      } catch {
        // A subscriber must not be able to break issuance.
      }
    }
    return event;
  }
}

/**
 * Test-only authority which accepts structural events without cryptography.
 * Never use this in a browser host.
 */
export class UnsafeUnverifiedAuthAuthority implements AuthEpochAuthority {
  readonly #events = new Map<string, AuthEpochEvent>();

  seed(event: AuthEpochEvent): void {
    this.#events.set(event.authSessionID, event);
  }

  issue(
    accountID: string,
    authSessionID: string,
    epoch: number,
    state: AuthEpochState,
  ): AuthEpochEvent {
    const event: AuthEpochEvent = {
      type: "AuthEpoch",
      accountID,
      authSessionID,
      epoch,
      state,
      alg: "Ed25519",
      keyID: "unsafe-test",
      signature: "dW5zYWZlLXRlc3Q=",
    };
    this.#events.set(authSessionID, event);
    return event;
  }

  verify(): boolean {
    return true;
  }

  readCurrent(authSessionID: string): AuthEpochEvent | undefined {
    return this.#events.get(authSessionID);
  }
}
