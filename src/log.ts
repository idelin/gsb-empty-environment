import {
  inspectMessageText,
  MessageValidationError,
  type CanonicalMessage,
  type Message,
  type MessageErrorCode,
  type SignatureVerifier,
} from "./message.ts";

export interface MessageLogOptions {
  /** The public identity that owns this log. */
  author: string;
  /** Caller-provided cryptographic signature check. */
  verify: SignatureVerifier;
}

export interface StoredMessage {
  readonly id: string;
  readonly sequence: number;
  readonly message: Message;
  readonly canonical: string;
}

export interface ForkEvidence {
  readonly author: string;
  readonly sequence: number;
  /** The first message accepted at this sequence. */
  readonly accepted: StoredMessage;
  /** The differently-identified message that exposed the fork. */
  readonly conflicting: StoredMessage;
}

export type IngestRejectionReason =
  | MessageErrorCode
  | "WRONG_AUTHOR"
  | "AUTHOR_FORKED"
  | "FORK"
  | "SEQUENCE_GAP"
  | "SEQUENCE_OUT_OF_ORDER"
  | "BROKEN_CHAIN";

export interface AcceptedIngest {
  readonly ok: true;
  readonly status: "accepted";
  readonly id: string;
  readonly stored: StoredMessage;
}

export interface DuplicateIngest {
  readonly ok: true;
  readonly status: "duplicate";
  readonly id: string;
  /** The existing stored entry; no second entry was appended. */
  readonly stored: StoredMessage;
}

export interface RejectedIngest {
  readonly ok: false;
  readonly status: "rejected";
  readonly reason: IngestRejectionReason;
  readonly detail: string;
  readonly candidate?: CanonicalMessage;
  readonly evidence?: ForkEvidence;
}

export type IngestResult = AcceptedIngest | DuplicateIngest | RejectedIngest;

function toStoredMessage(candidate: CanonicalMessage): StoredMessage {
  return {
    id: candidate.id,
    sequence: candidate.message.sequence,
    message: candidate.message,
    canonical: candidate.canonical,
  };
}

function rejected(
  reason: IngestRejectionReason,
  detail: string,
  extra: Pick<RejectedIngest, "candidate" | "evidence"> = {},
): RejectedIngest {
  return { ok: false, status: "rejected", reason, detail, ...extra };
}

/**
 * An append-only validator state for one author's log.
 *
 * It enforces canonical wire encoding, message IDs, chain links, sequence
 * continuity, and permanent fork quarantine. A log always starts at sequence
 * 1 with previous === null.
 */
export class MessageLog {
  readonly author: string;

  #verify: SignatureVerifier;
  #messages: StoredMessage[] = [];
  #bySequence = new Map<number, StoredMessage>();
  #forked = false;
  #forkEvidence: ForkEvidence[] = [];

  constructor(options: MessageLogOptions) {
    if (typeof options?.author !== "string" || options.author.length === 0) {
      throw new TypeError("author must be a non-empty string");
    }
    if (typeof options.verify !== "function") {
      throw new TypeError("verify must be a function");
    }

    this.author = options.author;
    this.#verify = options.verify;
  }

  get length(): number {
    return this.#messages.length;
  }

  get head(): StoredMessage | undefined {
    return this.#messages.at(-1);
  }

  get messages(): readonly StoredMessage[] {
    return this.#messages.slice();
  }

  get isForked(): boolean {
    return this.#forked;
  }

  get forkEvidence(): readonly ForkEvidence[] {
    return this.#forkEvidence.slice();
  }

  getForkEvidence(): readonly ForkEvidence[] {
    return this.forkEvidence;
  }

  /**
   * Ingest one exact wire-format message. The method is synchronous because
   * the injected verifier is synchronous.
   */
  ingest(text: string): IngestResult {
    let candidate: CanonicalMessage;

    try {
      candidate = inspectMessageText(text, this.#verify);
    } catch (error) {
      if (error instanceof MessageValidationError) {
        return rejected(error.code, error.message);
      }
      throw error;
    }

    if (candidate.message.author !== this.author) {
      return rejected(
        "WRONG_AUTHOR",
        `expected author ${this.author}, got ${candidate.message.author}`,
        { candidate },
      );
    }

    if (this.#forked) {
      return rejected(
        "AUTHOR_FORKED",
        "this author's log is quarantined after a fork",
        { candidate, evidence: this.#forkEvidence[0] },
      );
    }

    const sequence = candidate.message.sequence;
    const existing = this.#bySequence.get(sequence);

    // Check the sequence map before chain checks: a fork can otherwise be
    // misreported as a simple broken link.
    if (existing !== undefined) {
      if (existing.id === candidate.id) {
        return {
          ok: true,
          status: "duplicate",
          id: candidate.id,
          stored: existing,
        };
      }

      const evidence: ForkEvidence = {
        author: this.author,
        sequence,
        accepted: existing,
        conflicting: toStoredMessage(candidate),
      };
      this.#forked = true;
      this.#forkEvidence.push(evidence);

      return rejected(
        "FORK",
        `fork: sequence ${sequence} already has message ${existing.id}, incoming message has ${candidate.id}`,
        { candidate, evidence },
      );
    }

    const expectedSequence = this.#messages.length + 1;
    if (sequence > expectedSequence) {
      return rejected(
        "SEQUENCE_GAP",
        `expected sequence ${expectedSequence}, got ${sequence}`,
        { candidate },
      );
    }
    if (sequence < expectedSequence) {
      return rejected(
        "SEQUENCE_OUT_OF_ORDER",
        `expected sequence ${expectedSequence}, got already unobserved sequence ${sequence}`,
        { candidate },
      );
    }

    const expectedPrevious = this.head?.id ?? null;
    if (candidate.message.previous !== expectedPrevious) {
      return rejected(
        "BROKEN_CHAIN",
        `previous must be ${expectedPrevious ?? "null"}, got ${candidate.message.previous ?? "null"}`,
        { candidate },
      );
    }

    const stored = toStoredMessage(candidate);
    this.#messages.push(stored);
    this.#bySequence.set(sequence, stored);

    return {
      ok: true,
      status: "accepted",
      id: stored.id,
      stored,
    };
  }
}

export function createMessageLog(options: MessageLogOptions): MessageLog {
  return new MessageLog(options);
}

export { MessageLog as AuthorLog, MessageLog as LogValidator };
