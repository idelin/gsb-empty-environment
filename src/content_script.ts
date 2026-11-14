import {
  type SecurityConfig,
  isOperationAllowed,
  loadManifestConfig,
  parseTrustedConfigMessage,
} from "./config.ts";
import {
  OneTimePageBridge,
  type ContentWindowLike,
  type PrivilegedDispatcher,
} from "./bridge.ts";
import {
  findPageAnchor,
  type StructuralContainer,
  type StructuralSelection,
} from "./dom.ts";
import {
  ValidationError,
  isDataRecord,
  parseCanonicalOrigin,
} from "./security.ts";

export interface RuntimeMessageSender {
  readonly id?: unknown;
  readonly url?: unknown;
  readonly origin?: unknown;
}

export interface RuntimeMessageApi {
  addListener(
    listener: (
      message: unknown,
      sender: RuntimeMessageSender,
      sendResponse: (response?: unknown) => void,
    ) => boolean | void,
  ): void;
  removeListener?(
    listener: (
      message: unknown,
      sender: RuntimeMessageSender,
      sendResponse: (response?: unknown) => void,
    ) => boolean | void,
  ): void;
}

export interface RuntimeLike {
  readonly id: string;
  getManifest(): unknown;
  readonly onMessage: RuntimeMessageApi;
}

export interface ContentScriptOptions {
  readonly runtime: RuntimeLike;
  readonly hostWindow: ContentWindowLike;
  readonly documentLike: StructuralContainer;
  readonly dispatch: PrivilegedDispatcher;
  readonly now?: () => number;
}

export type TrustedMessageResult =
  | Readonly<{ accepted: true }>
  | Readonly<{ accepted: false; error: string }>;

/**
 * Orchestrates the isolated-world content script. Every privileged decision
 * is made from the manifest snapshot or a narrowing message whose sender is
 * verified by the extension runtime. Page DOM and window messages never
 * update #config.
 */
export class ExtensionContentScript {
  readonly #runtime: RuntimeLike;
  readonly #hostWindow: ContentWindowLike;
  readonly #documentLike: StructuralContainer;
  readonly #dispatch: PrivilegedDispatcher;
  readonly #bridge: OneTimePageBridge;

  #config: SecurityConfig;
  #started = false;

  readonly #runtimeListener = (
    message: unknown,
    sender: RuntimeMessageSender,
    _sendResponse: (response?: unknown) => void,
  ): void => {
    this.handleTrustedRuntimeMessage(message, sender);
  };

  constructor(options: ContentScriptOptions) {
    this.#runtime = options.runtime;
    this.#hostWindow = options.hostWindow;
    this.#documentLike = options.documentLike;
    this.#dispatch = options.dispatch;

    if (
      typeof this.#runtime.id !== "string" ||
      this.#runtime.id.length === 0
    ) {
      throw new ValidationError("invalid_value", "runtime.id is required");
    }

    this.#config = loadManifestConfig(this.#runtime.getManifest());

    const expectedOrigin = parseCanonicalOrigin(
      this.#hostWindow.location.origin,
      "hostWindow.location.origin",
    );
    this.#bridge = new OneTimePageBridge({
      hostWindow: this.#hostWindow,
      expectedOrigin,
      config: this.#config,
      dispatch: this.#dispatch,
      now: options.now,
    });
  }

  get config(): SecurityConfig {
    return this.#config;
  }

  start(): void {
    if (this.#started) {
      return;
    }
    this.#runtime.onMessage.addListener(this.#runtimeListener);
    if (this.#config.pageBridgeEnabled) {
      this.#bridge.start();
    }
    this.#started = true;
  }

  stop(): void {
    if (!this.#started) {
      this.#bridge.stop();
      return;
    }
    this.#runtime.onMessage.removeListener?.(this.#runtimeListener);
    this.#bridge.stop();
    this.#started = false;
  }

  /**
   * Intended for the optional, non-privileged page status mount only. The
   * returned element is never treated as configuration or authority.
   */
  locatePageAnchor(): StructuralSelection {
    return findPageAnchor(this.#documentLike);
  }

  handleTrustedRuntimeMessage(
    message: unknown,
    sender: RuntimeMessageSender,
  ): TrustedMessageResult {
    if (!isTrustedExtensionSender(sender, this.#runtime.id)) {
      return Object.freeze({
        accepted: false as const,
        error: "untrusted_sender",
      });
    }

    try {
      const next = parseTrustedConfigMessage(message, this.#config);
      this.#config = next;
      this.#bridge.updateConfig(next);

      if (next.pageBridgeEnabled) {
        if (this.#started) {
          this.#bridge.start();
        }
      } else {
        this.#bridge.stop();
      }
      return Object.freeze({ accepted: true as const });
    } catch (error) {
      if (error instanceof ValidationError) {
        return Object.freeze({
          accepted: false as const,
          error: error.code,
        });
      }
      return Object.freeze({
        accepted: false as const,
        error: "internal_error",
      });
    }
  }

  /**
   * A page can read this only if the extension explicitly exposes it. The
   * component never installs it on window and the value is a frozen snapshot.
   */
  isOperationEnabled(operation: string): boolean {
    return isOperationAllowed(this.#config, operation);
  }
}

/**
 * Sender checks are deliberately redundant. Pages that can use
 * externally_connectable do not carry the extension's runtime ID, and an
 * extension-page sender URL can never be an HTTP(S) page URL.
 */
export function isTrustedExtensionSender(
  sender: unknown,
  extensionId: string,
): boolean {
  if (!isDataRecord(sender)) {
    return false;
  }
  const idDescriptor = Object.getOwnPropertyDescriptor(sender, "id");
  if (
    idDescriptor === undefined ||
    !("value" in idDescriptor) ||
    typeof idDescriptor.value !== "string" ||
    idDescriptor.value !== extensionId
  ) {
    return false;
  }

  const urlDescriptor = Object.getOwnPropertyDescriptor(sender, "url");
  if (urlDescriptor !== undefined) {
    if (
      !("value" in urlDescriptor) ||
      typeof urlDescriptor.value !== "string" ||
      !urlDescriptor.value.startsWith(`chrome-extension://${extensionId}/`)
    ) {
      return false;
    }
  }

  const originDescriptor = Object.getOwnPropertyDescriptor(sender, "origin");
  if (originDescriptor !== undefined) {
    if (
      !("value" in originDescriptor) ||
      typeof originDescriptor.value !== "string" ||
      originDescriptor.value !== `chrome-extension://${extensionId}`
    ) {
      return false;
    }
  }
  return true;
}
