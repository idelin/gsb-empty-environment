import { InvalidLastEventIdError } from "./errors.ts";
import {
  issueEventId,
  parseEventId,
  verifyEventId,
  type SseSecret,
  type StreamIdentity,
} from "./cursor.ts";
import { resetEvent, type SseEvent } from "./events.ts";

export const DEFAULT_RETENTION_WINDOW = 1000;

export type LastEventIdValue = string | string[] | null | undefined;

export interface LastEventIdInput {
  headerLastEventId?: LastEventIdValue;
  queryLastEventId?: LastEventIdValue;
}

export interface ResumeOptions extends LastEventIdInput {
  subscription: StreamIdentity;
  secret: SseSecret;
  /** Sequence number of the newest event currently retained by the stream. */
  latestSequence: number;
  /** Number of recent events that can still be replayed. Defaults to 1000. */
  retentionWindow?: number;
}

interface RejectedResumeDecision {
  action: "reject";
  statusCode: 400;
  nextSequence: null;
}

interface StartResumeDecision {
  action: "start";
  nextSequence: number;
  resetEvent: null;
}

interface ResetResumeDecision {
  action: "reset";
  nextSequence: number;
  resetEvent: Readonly<SseEvent>;
}

interface ReplayResumeDecision {
  action: "resume";
  nextSequence: number;
  resetEvent: null;
}

export type ResumeDecision =
  | RejectedResumeDecision
  | StartResumeDecision
  | ResetResumeDecision
  | ReplayResumeDecision;

interface PresentValue {
  present: boolean;
  value?: string;
}

function normalizeOneValue(value: LastEventIdValue): PresentValue {
  if (value === null || value === undefined) {
    return { present: false };
  }

  if (Array.isArray(value)) {
    if (value.length === 0) {
      return { present: false };
    }
    if (value.length > 1) {
      throw new InvalidLastEventIdError();
    }
    value = value[0];
  }

  if (typeof value !== "string") {
    throw new InvalidLastEventIdError();
  }

  return { present: true, value };
}

function normalizeLastEventId(input: LastEventIdInput): string | undefined {
  const header = normalizeOneValue(input.headerLastEventId);
  const query = normalizeOneValue(input.queryLastEventId);

  if (header.present && query.present && header.value !== query.value) {
    throw new InvalidLastEventIdError();
  }

  return header.present ? header.value : query.value;
}

function invalidDecision(): RejectedResumeDecision {
  return { action: "reject", statusCode: 400, nextSequence: null };
}

function startDecision(latestSequence: number): StartResumeDecision {
  return {
    action: "start",
    nextSequence: latestSequence + 1,
    resetEvent: null,
  };
}

function resetDecision(latestSequence: number): ResetResumeDecision {
  return {
    action: "reset",
    nextSequence: latestSequence + 1,
    resetEvent: resetEvent(),
  };
}

function assertResumeOptions(options: ResumeOptions): {
  retentionWindow: number;
} {
  if (
    !Number.isSafeInteger(options.latestSequence) ||
    options.latestSequence < -1
  ) {
    throw new TypeError(
      "latestSequence must be a safe integer greater than or equal to -1",
    );
  }

  const retentionWindow =
    options.retentionWindow === undefined
      ? DEFAULT_RETENTION_WINDOW
      : options.retentionWindow;
  if (!Number.isSafeInteger(retentionWindow) || retentionWindow < 1) {
    throw new TypeError("retentionWindow must be a positive safe integer");
  }

  return { retentionWindow };
}

/**
 * Decides how a subscription continues.
 *
 * - No cursor: start at the current tail without a reset.
 * - Valid cursor inside the replay window: resume after that sequence.
 * - Bad signature, wrong stream, or evicted sequence: reset to the current tail.
 * - Unsafe or ambiguous transport value: reject with a generic 400.
 *
 * Invalid cursors never carry a public diagnostic reason.
 */
export function resolveResume(options: ResumeOptions): ResumeDecision {
  const { retentionWindow } = assertResumeOptions(options);

  let rawCursor: string | undefined;
  try {
    rawCursor = normalizeLastEventId(options);
  } catch {
    return invalidDecision();
  }

  if (rawCursor === undefined) {
    return startDecision(options.latestSequence);
  }

  let parsed: ReturnType<typeof parseEventId>;
  try {
    parsed = parseEventId(rawCursor);
  } catch {
    return invalidDecision();
  }

  let valid = false;
  try {
    valid = verifyEventId(rawCursor, options.subscription, options.secret);
  } catch {
    return invalidDecision();
  }

  if (
    !valid ||
    parsed.stream !== options.subscription.stream ||
    options.latestSequence < 0
  ) {
    return resetDecision(options.latestSequence);
  }

  const oldestRetained = Math.max(
    0,
    options.latestSequence - (retentionWindow - 1),
  );
  if (
    parsed.sequence < oldestRetained ||
    parsed.sequence > options.latestSequence
  ) {
    return resetDecision(options.latestSequence);
  }

  return {
    action: "resume",
    nextSequence: parsed.sequence + 1,
    resetEvent: null,
  };
}

export const resolveLastEventId = resolveResume;

export interface SseResumeOptions {
  secret: SseSecret;
  retentionWindow?: number;
}

/**
 * Stateful convenience wrapper for servers that want a fixed signing secret.
 * The top-level functions remain available for dependency-injection-style use.
 */
export class SseResume {
  readonly secret: SseSecret;
  readonly retentionWindow: number;

  constructor(options: SseResumeOptions) {
    const retentionWindow =
      options.retentionWindow ?? DEFAULT_RETENTION_WINDOW;
    if (!Number.isSafeInteger(retentionWindow) || retentionWindow < 1) {
      throw new TypeError("retentionWindow must be a positive safe integer");
    }
    // Validate the key once even if no ID is issued yet.
    issueEventId({
      stream: "probe",
      sequence: 0,
      tenant: "probe",
      secret: options.secret,
    });
    this.secret = options.secret;
    this.retentionWindow = retentionWindow;
  }

  issueEventId(subscription: StreamIdentity, sequence: number): string {
    return issueEventId({
      ...subscription,
      sequence,
      secret: this.secret,
    });
  }

  resolve(
    subscription: StreamIdentity,
    request: LastEventIdInput,
    latestSequence: number,
  ): ResumeDecision {
    return resolveResume({
      subscription,
      ...request,
      latestSequence,
      secret: this.secret,
      retentionWindow: this.retentionWindow,
    });
  }
}

export interface HttpLastEventIdInput {
  headers?:
    | Headers
    | Record<string, string | string[] | undefined>
    | { get(name: string): string | null };
  url?: string | URL;
}

function headerValue(
  headers: HttpLastEventIdInput["headers"],
  name: string,
): LastEventIdValue {
  if (headers === undefined) {
    return undefined;
  }

  if (typeof (headers as { get?: unknown }).get === "function") {
    return (headers as { get(name: string): string | null }).get(name);
  }

  const record = headers as Record<string, string | string[] | undefined>;
  const key = Object.keys(record).find(
    (candidate) => candidate.toLowerCase() === name,
  );
  return key === undefined ? undefined : record[key];
}

/**
 * Extracts both cursor sources from a Fetch-style request without choosing one
 * silently. URLSearchParams performs the required query decoding.
 */
export function extractLastEventIdRequest(
  input: HttpLastEventIdInput,
): LastEventIdInput {
  let query: LastEventIdValue;
  if (input.url !== undefined) {
    const url =
      input.url instanceof URL
        ? input.url
        : new URL(input.url, "http://localhost");
    const values = url.searchParams.getAll("lastEventId");
    query =
      values.length === 0 ? undefined : values.length === 1 ? values[0] : values;
  }

  return {
    headerLastEventId: headerValue(input.headers, "last-event-id"),
    queryLastEventId: query,
  };
}
