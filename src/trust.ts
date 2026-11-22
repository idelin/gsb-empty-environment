import { UntrustedSelectionError } from "./errors.ts";

export interface TrustedSelectionEvent {
  readonly kind: "selection";
  readonly objectIds: readonly string[];
}

export interface TrustedCancelEvent {
  readonly kind: "cancel";
}

export type TrustedPickerEvent = TrustedSelectionEvent | TrustedCancelEvent;

const trustedEvents = new WeakSet<object>();
const consumedEvents = new WeakSet<object>();

/**
 * Trusted integrator ("browser chrome") handle. Code that holds a BrowserTrust
 * is allowed to turn user gestures into picker events. The events it mints are
 * opaque, frozen, single-use objects; a plain { kind, objectIds } literal or a
 * path string can never be mistaken for one.
 */
export class BrowserTrust {
  selectionEvent(objectIds: readonly string[]): TrustedSelectionEvent {
    const event: TrustedSelectionEvent = Object.freeze({
      kind: "selection",
      objectIds: Object.freeze([...objectIds]),
    });
    trustedEvents.add(event);
    return event;
  }

  cancelEvent(): TrustedCancelEvent {
    const event: TrustedCancelEvent = Object.freeze({ kind: "cancel" });
    trustedEvents.add(event);
    return event;
  }
}

/**
 * Module-internal: validate that an event was minted by a BrowserTrust and has
 * not been applied before. Consumes the event so it cannot be replayed.
 */
export function claimTrustedEvent(event: unknown): TrustedPickerEvent {
  if (
    event === null ||
    (typeof event !== "object" && typeof event !== "function") ||
    !trustedEvents.has(event as object)
  ) {
    throw new UntrustedSelectionError(
      "selection event was not minted by trusted browser code",
    );
  }
  if (consumedEvents.has(event as object)) {
    throw new UntrustedSelectionError(
      "selection event has already been consumed (replay rejected)",
    );
  }
  consumedEvents.add(event as object);
  return event as TrustedPickerEvent;
}
