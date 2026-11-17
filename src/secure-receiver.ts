/**
 * Secure host-side message receiver.
 *
 * Threat model: after the host validates `event.origin`, the sender window
 * may navigate to a different document (same Window object reused). A reply
 * sent later through the raw window reference would then be delivered to the
 * new document and leak content meant for the original sender.
 *
 * Defense: when a message is accepted, the receiver issues a ReplyCapability
 * bound to the immutable, platform-stamped trusted metadata (send-time origin
 * + document generation). The capability replies only while that exact
 * document still occupies the window; navigation kills the capability, so
 * window-object reuse never inherits it.
 */

import type {
  DocumentGeneration,
  MessageListener,
  Platform,
  PlatformMessageEvent,
  SimulatedWindow,
  TrustedMessageMeta,
} from './platform.ts';

export interface SecureMessage {
  readonly data: unknown;
  readonly meta: TrustedMessageMeta;
  /** Returns true only if the reply was queued for the original document. */
  readonly reply: (data: unknown) => boolean;
}

export type SecureMessageHandler = (message: SecureMessage) => void;

export interface SecureReceiverOptions {
  readonly allowedOrigins: ReadonlyArray<string>;
}

export class ReplyCapability {
  #platform: Platform;
  #host: SimulatedWindow;
  #source: SimulatedWindow;
  #origin: string;
  #generation: DocumentGeneration;

  constructor(
    platform: Platform,
    host: SimulatedWindow,
    source: SimulatedWindow,
    meta: TrustedMessageMeta,
  ) {
    this.#platform = platform;
    this.#host = host;
    this.#source = source;
    this.#origin = meta.origin;
    this.#generation = meta.generation;
  }

  /** True only while the source window still hosts the original document. */
  get valid(): boolean {
    return this.#source.currentGeneration === this.#generation;
  }

  /**
   * Sends a reply to the original document only. If the sender window has
   * navigated (same or different origin), the reply is dropped and false is
   * returned; the new document never observes the payload.
   */
  reply(data: unknown): boolean {
    if (!this.valid) return false;
    return this.#platform.postMessageToDocument(
      this.#host,
      this.#source,
      data,
      this.#origin,
      this.#generation,
    );
  }
}

export class SecureMessageReceiver {
  #platform: Platform;
  #window: SimulatedWindow;
  #allowedOrigins: ReadonlySet<string>;
  #handler: SecureMessageHandler;
  #listener: MessageListener;

  constructor(
    platform: Platform,
    window: SimulatedWindow,
    options: SecureReceiverOptions,
    handler: SecureMessageHandler,
  ) {
    this.#platform = platform;
    this.#window = window;
    this.#allowedOrigins = new Set(options.allowedOrigins);
    this.#handler = handler;
    this.#listener = (event) => this.#onMessage(event);
    this.#window.addMessageListener(this.#listener);
  }

  detach(): void {
    this.#window.removeMessageListener(this.#listener);
  }

  #onMessage(event: PlatformMessageEvent): void {
    if (!this.#platform.isGenuineMessage(event)) return;
    const meta = event.trustedMeta;
    if (!this.#allowedOrigins.has(meta.origin)) return;
    const capability = new ReplyCapability(this.#platform, this.#window, event.source, meta);
    const reply = (data: unknown): boolean => capability.reply(data);
    this.#handler(Object.freeze({ data: event.data, meta, reply }));
  }
}
