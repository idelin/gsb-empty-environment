export interface SseEvent {
  id?: string | number;
  event?: string;
  data?: unknown;
  retry?: number;
}

const RESET_EVENT: Readonly<SseEvent> = Object.freeze({
  event: "reset",
  data: "",
});

function splitSseLines(value: string): string[] {
  return value.replace(/\r\n|\r/g, "\n").split("\n");
}

function field(name: string, value: string): string {
  return splitSseLines(value)
    .map((line) => `${name}: ${line}`)
    .join("\n");
}

function dataToText(data: unknown): string | undefined {
  if (data === undefined) {
    return undefined;
  }
  if (typeof data === "string") {
    return data;
  }
  return JSON.stringify(data);
}

/**
 * Serializes an event without allowing CR, LF, or NUL in a supplied value to
 * create another SSE field. Every logical line is emitted with the same field
 * prefix; unknown object properties are never inspected.
 */
export function formatSseEvent(event: SseEvent): string {
  if (event === null || typeof event !== "object") {
    throw new TypeError("event must be an object");
  }

  const lines: string[] = [];

  if (event.event !== undefined) {
    if (typeof event.event !== "string") {
      throw new TypeError("event.event must be a string");
    }
    lines.push(field("event", event.event));
  }

  if (event.id !== undefined) {
    if (typeof event.id !== "string" && typeof event.id !== "number") {
      throw new TypeError("event.id must be a string or number");
    }
    lines.push(field("id", String(event.id)));
  }

  const data = dataToText(event.data);
  if (data !== undefined) {
    lines.push(field("data", data));
  }

  if (event.retry !== undefined) {
    if (!Number.isSafeInteger(event.retry) || event.retry < 0) {
      throw new TypeError("event.retry must be a non-negative safe integer");
    }
    lines.push(`retry: ${event.retry}`);
  }

  return lines.length === 0 ? "" : `${lines.join("\n")}\n\n`;
}

export const serializeSseEvent = formatSseEvent;

export function resetEvent(): Readonly<SseEvent> {
  return RESET_EVENT;
}

/**
 * The reset event intentionally carries no ID and no diagnostic reason. It is
 * only a private signal telling the client that its cursor was discarded.
 */
export function formatResetEvent(): string {
  return formatSseEvent(RESET_EVENT);
}

export const serializeResetEvent = formatResetEvent;
