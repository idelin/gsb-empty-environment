export type TrackKind = "audio" | "video";

export type DisplaySurface = "application" | "browser" | "monitor" | "window";

/**
 * The immutable authorization tuple captured when the user agrees to share a
 * source. A track may only be sent (or replace the current track) while its
 * kind, deviceId and displaySurface still match this record.
 */
export interface SendTrackConsent {
  readonly id: string;
  readonly kind: TrackKind;
  readonly deviceId: string;
  readonly displaySurface?: DisplaySurface;
}

export interface ShareConsentOptions {
  id?: string;
  /**
   * Some display-capture implementations omit deviceId from getSettings().
   * Callers may supply the source identifier they recorded when the user
   * approved the share.
   */
  deviceId?: string;
  /** Defaults to settings.displaySurface. */
  displaySurface?: DisplaySurface;
}

export type TrackGuardErrorCode =
  | "invalid-consent"
  | "settings-unavailable"
  | "kind-mismatch"
  | "device-mismatch"
  | "display-surface-mismatch"
  | "track-not-live"
  | "guard-paused"
  | "guard-disposed"
  | "resume-failed";

export class TrackGuardError extends Error {
  readonly code: TrackGuardErrorCode;
  readonly consent?: SendTrackConsent;

  constructor(
    code: TrackGuardErrorCode,
    message: string,
    consent?: SendTrackConsent,
    cause?: unknown,
  ) {
    super(message, cause === undefined ? undefined : { cause });
    this.name = "TrackGuardError";
    this.code = code;
    this.consent = consent;
  }
}

export type PauseReason =
  | "invalid-replacement"
  | "settings-changed"
  | "ready-state-abnormal"
  | "manual";

export interface TrackGuardViolation {
  readonly reason: PauseReason;
  readonly message: string;
  readonly attemptedTrack?: MediaStreamTrack | null;
  readonly error?: TrackGuardError;
}

export interface SendTrackGuardOptions {
  /** Defaults to sender.track when the sender already has a track. */
  initialTrack?: MediaStreamTrack | null;
  /**
   * Replace the sender instance's replaceTrack method with the guarded one.
   * Enabled by default. This stops callers that hold the sender object from
   * bypassing the guard with a direct replaceTrack call.
   */
  patchSender?: boolean;
  onViolation?: (violation: TrackGuardViolation) => void;
}

export interface SendTrackGuard {
  readonly consent: Readonly<SendTrackConsent>;
  readonly paused: boolean;
  readonly pauseReason: PauseReason | null;
  readonly currentTrack: MediaStreamTrack | null;
  readonly lastViolation: TrackGuardViolation | null;

  /** Validates the candidate before it is allowed to replace the live track. */
  replaceTrack(track: MediaStreamTrack | null): Promise<void>;
  /** Re-attaches the last safe track after an explicit review. */
  resume(): Promise<void>;
  /** Stops sending and keeps the last safe track available for resume(). */
  pause(message?: string): Promise<void>;
  /** Re-checks the current track and pauses on a mismatch. */
  verify(): Promise<boolean>;
  dispose(): void;
}

const DISPLAY_SURFACES = new Set<DisplaySurface>([
  "application",
  "browser",
  "monitor",
  "window",
]);

let fallbackConsentSequence = 0;

function nextConsentId(): string {
  const cryptoObject = (
    globalThis as { crypto?: { randomUUID?: () => string } }
  ).crypto;
  return (
    cryptoObject?.randomUUID?.() ??
    `consent-${Date.now()}-${++fallbackConsentSequence}`
  );
}

function isDisplaySurface(value: unknown): value is DisplaySurface {
  return (
    typeof value === "string" &&
    DISPLAY_SURFACES.has(value as DisplaySurface)
  );
}

function readSettings(track: MediaStreamTrack): MediaTrackSettings {
  if (typeof track.getSettings !== "function") {
    throw new TrackGuardError(
      "settings-unavailable",
      "The track does not expose getSettings().",
    );
  }

  try {
    const settings = track.getSettings();
    if (!settings || typeof settings !== "object") {
      throw new TypeError("getSettings() did not return an object.");
    }
    return settings;
  } catch (cause) {
    if (cause instanceof TrackGuardError) {
      throw cause;
    }
    throw new TrackGuardError(
      "settings-unavailable",
      "The track settings could not be read.",
      undefined,
      cause,
    );
  }
}

function assertConsent(consent: SendTrackConsent): void {
  if (!consent || typeof consent !== "object") {
    throw new TrackGuardError("invalid-consent", "A consent record is required.");
  }
  if (typeof consent.id !== "string" || consent.id.length === 0) {
    throw new TrackGuardError(
      "invalid-consent",
      "The consent record must have a non-empty id.",
    );
  }
  if (consent.kind !== "audio" && consent.kind !== "video") {
    throw new TrackGuardError(
      "invalid-consent",
      `Unsupported consent kind: ${String(consent.kind)}.`,
    );
  }
  if (typeof consent.deviceId !== "string" || consent.deviceId.length === 0) {
    throw new TrackGuardError(
      "invalid-consent",
      "The consent record must have a non-empty deviceId.",
    );
  }
  if (
    consent.displaySurface !== undefined &&
    !isDisplaySurface(consent.displaySurface)
  ) {
    throw new TrackGuardError(
      "invalid-consent",
      `Unsupported displaySurface: ${String(consent.displaySurface)}.`,
    );
  }
}

function describeSettings(settings: MediaTrackSettings): string {
  return JSON.stringify({
    deviceId: settings.deviceId,
    displaySurface: settings.displaySurface,
  });
}

/**
 * Capture the authorization tuple from the track the user just approved.
 * The returned record is frozen so later application code cannot widen it.
 */
export function createShareConsent(
  track: MediaStreamTrack,
  options: ShareConsentOptions = {},
): Readonly<SendTrackConsent> {
  if (!track || (track.kind !== "audio" && track.kind !== "video")) {
    throw new TrackGuardError(
      "invalid-consent",
      "Only an audio or video MediaStreamTrack can be approved.",
    );
  }

  const settings = readSettings(track);
  const deviceId = options.deviceId ?? settings.deviceId;
  if (typeof deviceId !== "string" || deviceId.length === 0) {
    throw new TrackGuardError(
      "invalid-consent",
      "The approved track has no deviceId. Record the capture source id explicitly.",
    );
  }

  const displaySurface = options.displaySurface ?? settings.displaySurface;
  if (displaySurface !== undefined && !isDisplaySurface(displaySurface)) {
    throw new TrackGuardError(
      "invalid-consent",
      `Unsupported displaySurface: ${String(displaySurface)}.`,
    );
  }
  if (track.kind === "audio" && displaySurface !== undefined) {
    throw new TrackGuardError(
      "invalid-consent",
      "An audio consent record cannot have a displaySurface.",
    );
  }

  const consent: SendTrackConsent = {
    id: options.id ?? nextConsentId(),
    kind: track.kind,
    deviceId,
    ...(displaySurface === undefined ? {} : { displaySurface }),
  };
  assertConsent(consent);
  return Object.freeze(consent);
}

/**
 * Throws when a track falls outside the approved kind/device/surface tuple.
 */
export function assertTrackMatchesConsent(
  track: MediaStreamTrack,
  consent: SendTrackConsent,
  options: { requireLive?: boolean } = {},
): void {
  assertConsent(consent);

  if (!track || typeof track !== "object") {
    throw new TrackGuardError(
      "kind-mismatch",
      "A MediaStreamTrack is required.",
      consent,
    );
  }
  if (track.kind !== consent.kind) {
    throw new TrackGuardError(
      "kind-mismatch",
      `Expected a ${consent.kind} track, received ${String(track.kind)}.`,
      consent,
    );
  }
  if (options.requireLive !== false && track.readyState !== "live") {
    throw new TrackGuardError(
      "track-not-live",
      `The track readyState is ${String(track.readyState)}, not "live".`,
      consent,
    );
  }

  const settings = readSettings(track);
  if (settings.deviceId !== consent.deviceId) {
    throw new TrackGuardError(
      "device-mismatch",
      `Consent ${consent.id} is bound to deviceId ${consent.deviceId}; the track reports ${describeSettings(settings)}.`,
      consent,
    );
  }

  if (consent.displaySurface === undefined) {
    if (settings.displaySurface !== undefined) {
      throw new TrackGuardError(
        "display-surface-mismatch",
        `Consent ${consent.id} has no displaySurface; the track reports ${describeSettings(settings)}.`,
        consent,
      );
    }
  } else if (settings.displaySurface !== consent.displaySurface) {
    throw new TrackGuardError(
      "display-surface-mismatch",
      `Consent ${consent.id} is bound to displaySurface "${consent.displaySurface}"; the track reports ${describeSettings(settings)}.`,
      consent,
    );
  }
}

export function trackMatchesConsent(
  track: MediaStreamTrack,
  consent: SendTrackConsent,
  options: { requireLive?: boolean } = {},
): boolean {
  try {
    assertTrackMatchesConsent(track, consent, options);
    return true;
  } catch {
    return false;
  }
}

function isTrackLike(
  value: MediaStreamTrack | null | undefined,
): value is MediaStreamTrack {
  return (
    value !== null &&
    value !== undefined &&
    typeof value === "object" &&
    typeof (value as { getSettings?: unknown }).getSettings === "function"
  );
}

/**
 * Create a fail-closed outbound track guard for one RTCRtpSender.
 *
 * The sender's replaceTrack method is patched by default. A rejected
 * replacement never reaches the native sender; instead the guard disables the
 * approved track and detaches it from the sender.
 */
export function createSendTrackGuard(
  sender: RTCRtpSender,
  consent: SendTrackConsent,
  options: SendTrackGuardOptions = {},
): SendTrackGuard {
  assertConsent(consent);
  const boundConsent = Object.freeze({ ...consent });

  const originalMethod = sender.replaceTrack;
  const nativeReplace = originalMethod.bind(sender);
  const initialTrack =
    options.initialTrack !== undefined
      ? options.initialTrack
      : ((sender as { track?: MediaStreamTrack | null }).track ?? null);

  let boundTrack: MediaStreamTrack | null = null;
  let lastSentTrack: MediaStreamTrack | null = null;
  let paused = false;
  let pauseReason: PauseReason | null = null;
  let lastViolation: TrackGuardViolation | null = null;
  let disposed = false;
  let senderPatched = false;
  let stateRevision = 0;
  let enabledBeforePause: boolean | undefined;
  let removeTrackListeners: (() => void) | null = null;

  function report(violation: TrackGuardViolation): void {
    try {
      options.onViolation?.(violation);
    } catch {
      // A faulty observer must not keep the track sending.
    }
  }

  function unbindTrackListeners(): void {
    removeTrackListeners?.();
    removeTrackListeners = null;
  }

  function bindTrackListeners(track: MediaStreamTrack): void {
    unbindTrackListeners();
    if (disposed) {
      return;
    }

    const onSettingsChange = (): void => {
      if (disposed || paused) {
        return;
      }
      let error: TrackGuardError | undefined;
      try {
        assertTrackMatchesConsent(track, boundConsent, { requireLive: false });
      } catch (cause) {
        if (cause instanceof TrackGuardError) {
          error = cause;
        }
      }
      void pauseGuard("settings-changed", {
        message:
          "Track settings changed after consent; sending was paused for review.",
        error,
        attemptedTrack: track,
      });
    };

    const onEnded = (): void => {
      if (disposed || paused) {
        return;
      }
      void pauseGuard("ready-state-abnormal", {
        message: `Track readyState became ${String(track.readyState)}; sending was paused.`,
        attemptedTrack: track,
      });
    };

    const onStateEvent = (): void => {
      if (disposed || paused || track.readyState === "live") {
        return;
      }
      void pauseGuard("ready-state-abnormal", {
        message: `Track readyState is ${String(track.readyState)}; sending was paused.`,
        attemptedTrack: track,
      });
    };

    track.addEventListener("settingschange", onSettingsChange);
    track.addEventListener("ended", onEnded);
    track.addEventListener("mute", onStateEvent);
    track.addEventListener("unmute", onStateEvent);

    removeTrackListeners = () => {
      track.removeEventListener("settingschange", onSettingsChange);
      track.removeEventListener("ended", onEnded);
      track.removeEventListener("mute", onStateEvent);
      track.removeEventListener("unmute", onStateEvent);
    };
  }

  async function pauseGuard(
    reason: PauseReason,
    detail: {
      message: string;
      attemptedTrack?: MediaStreamTrack | null;
      error?: TrackGuardError;
    },
    force = false,
  ): Promise<void> {
    if (disposed) {
      return;
    }
    if (paused && !force) {
      // Cancel any attach/resume that was already in flight.
      stateRevision += 1;
      try {
        await nativeReplace(null);
      } catch {
        // Keep the existing paused state.
      }
      return;
    }

    stateRevision += 1;
    paused = true;
    pauseReason = reason;

    const track = boundTrack;
    if (track) {
      const enabled = (track as { enabled?: boolean }).enabled;
      if (typeof enabled === "boolean") {
        if (enabledBeforePause === undefined) {
          enabledBeforePause = enabled;
        }
        track.enabled = false;
      }
    }
    unbindTrackListeners();

    const violation: TrackGuardViolation = {
      reason,
      message: detail.message,
      ...(detail.attemptedTrack === undefined
        ? {}
        : { attemptedTrack: detail.attemptedTrack }),
      ...(detail.error === undefined ? {} : { error: detail.error }),
    };
    lastViolation = violation;
    report(violation);

    try {
      await nativeReplace(null);
    } catch {
      // The local track is already disabled. There is nothing else safe to do
      // if the sender itself cannot be cleared.
    }
  }

  async function replaceTrack(track: MediaStreamTrack | null): Promise<void> {
    if (disposed) {
      throw new TrackGuardError(
        "guard-disposed",
        "This consent guard has been disposed.",
      );
    }

    if (track === null) {
      await pauseGuard("manual", {
        message: "Sending was paused because the caller detached the track.",
      });
      return;
    }

    if (paused) {
      throw new TrackGuardError(
        "guard-paused",
        "Sending is paused. Review the consent and call resume() first.",
        boundConsent,
      );
    }

    let validationError: TrackGuardError | undefined;
    try {
      assertTrackMatchesConsent(track, boundConsent, { requireLive: true });
    } catch (cause) {
      validationError =
        cause instanceof TrackGuardError
          ? cause
          : new TrackGuardError(
              "settings-unavailable",
              "The replacement track could not be validated.",
              boundConsent,
              cause,
            );
    }

    if (validationError) {
      await pauseGuard("invalid-replacement", {
        message: `Replacement refused: ${validationError.message}`,
        attemptedTrack: track,
        error: validationError,
      });
      throw validationError;
    }

    const revisionBeforeAttach = stateRevision;
    await nativeReplace(track);
    if (disposed || paused || stateRevision !== revisionBeforeAttach) {
      await nativeReplace(null).catch(() => {});
      throw new TrackGuardError(
        disposed ? "guard-disposed" : "guard-paused",
        disposed
          ? "This consent guard was disposed while replacing the track."
          : "Sending was paused while the replacement was in flight.",
        boundConsent,
      );
    }

    boundTrack = track;

    // Re-check after crossing the async sender boundary. If the source changed
    // while the native call was in flight, fail closed immediately.
    try {
      assertTrackMatchesConsent(track, boundConsent, { requireLive: true });
    } catch (cause) {
      const error =
        cause instanceof TrackGuardError
          ? cause
          : new TrackGuardError(
              "settings-unavailable",
              "The replacement track could not be validated.",
              boundConsent,
              cause,
            );
      await pauseGuard(
        "invalid-replacement",
        {
          message: `Replacement failed post-attach validation: ${error.message}`,
          attemptedTrack: track,
          error,
        },
        true,
      );
      throw error;
    }

    lastSentTrack = track;
    enabledBeforePause = undefined;
    bindTrackListeners(track);
  }

  async function resume(): Promise<void> {
    if (disposed) {
      throw new TrackGuardError(
        "guard-disposed",
        "This consent guard has been disposed.",
      );
    }
    if (!paused) {
      return;
    }

    const track = lastSentTrack ?? boundTrack;
    if (!track) {
      throw new TrackGuardError(
        "resume-failed",
        "There is no approved track to resume.",
        boundConsent,
      );
    }

    try {
      assertTrackMatchesConsent(track, boundConsent, { requireLive: true });
    } catch (cause) {
      const error =
        cause instanceof TrackGuardError
          ? cause
          : new TrackGuardError(
              "resume-failed",
              "The approved track no longer matches the consent record.",
              boundConsent,
              cause,
            );
      throw new TrackGuardError(
        "resume-failed",
        `Sending stays paused: ${error.message}`,
        boundConsent,
        error,
      );
    }

    const revisionBeforeAttach = stateRevision;
    await nativeReplace(track);
    if (disposed || stateRevision !== revisionBeforeAttach) {
      await nativeReplace(null).catch(() => {});
      throw new TrackGuardError(
        disposed ? "guard-disposed" : "guard-paused",
        disposed
          ? "This consent guard was disposed while resuming the track."
          : "Sending was paused while resume was in flight.",
        boundConsent,
      );
    }

    try {
      assertTrackMatchesConsent(track, boundConsent, { requireLive: true });
    } catch (cause) {
      const error =
        cause instanceof TrackGuardError
          ? cause
          : new TrackGuardError(
              "resume-failed",
              "The approved track changed while resuming.",
              boundConsent,
              cause,
            );
      await pauseGuard(
        "ready-state-abnormal",
        {
          message: `Resume failed post-attach validation: ${error.message}`,
          attemptedTrack: track,
          error,
        },
        true,
      );
      throw new TrackGuardError(
        "resume-failed",
        `Sending stays paused: ${error.message}`,
        boundConsent,
        error,
      );
    }

    boundTrack = track;
    lastSentTrack = track;
    if (
      enabledBeforePause !== undefined &&
      typeof (track as { enabled?: boolean }).enabled === "boolean"
    ) {
      track.enabled = enabledBeforePause;
    }
    enabledBeforePause = undefined;
    paused = false;
    pauseReason = null;
    lastViolation = null;
    bindTrackListeners(track);
  }

  async function verify(): Promise<boolean> {
    if (disposed || paused) {
      return false;
    }
    const track = boundTrack ?? lastSentTrack;
    if (!track) {
      return true;
    }

    try {
      assertTrackMatchesConsent(track, boundConsent, { requireLive: true });
      return true;
    } catch (cause) {
      const error =
        cause instanceof TrackGuardError
          ? cause
          : new TrackGuardError(
              "settings-unavailable",
              "The current track could not be validated.",
              boundConsent,
              cause,
            );
      await pauseGuard(
        error.code === "track-not-live"
          ? "ready-state-abnormal"
          : "settings-changed",
        {
          message: `Current track failed verification: ${error.message}`,
          attemptedTrack: track,
          error,
        },
      );
      return false;
    }
  }

  function dispose(): void {
    if (disposed) {
      return;
    }
    disposed = true;
    stateRevision += 1;
    paused = true;
    unbindTrackListeners();

    if (senderPatched) {
      try {
        Object.defineProperty(sender, "replaceTrack", {
          configurable: true,
          enumerable: false,
          writable: true,
          value: originalMethod,
        });
      } catch {
        // Best effort: some host objects do not permit restoring own props.
      }
      senderPatched = false;
    }
  }

  const guard: SendTrackGuard = {
    consent: boundConsent,
    get paused() {
      return paused;
    },
    get pauseReason() {
      return pauseReason;
    },
    get currentTrack() {
      return lastSentTrack;
    },
    get lastViolation() {
      return lastViolation;
    },
    replaceTrack,
    resume,
    pause(message = "Sending was paused manually.") {
      return pauseGuard("manual", { message });
    },
    verify,
    dispose,
  };

  if (initialTrack !== null && initialTrack !== undefined) {
    if (!isTrackLike(initialTrack)) {
      throw new TrackGuardError(
        "invalid-consent",
        "The initial sender track is not a MediaStreamTrack.",
        boundConsent,
      );
    }
    boundTrack = initialTrack;
    lastSentTrack = initialTrack;
    if (!trackMatchesConsent(initialTrack, boundConsent, { requireLive: true })) {
      void pauseGuard("invalid-replacement", {
        message:
          "The track already attached to the sender is outside the consent record; sending was paused.",
        attemptedTrack: initialTrack,
      });
    } else {
      bindTrackListeners(initialTrack);
    }
  }

  if (options.patchSender !== false) {
    try {
      Object.defineProperty(sender, "replaceTrack", {
        configurable: true,
        enumerable: false,
        writable: false,
        value: replaceTrack,
      });
      senderPatched = true;
    } catch {
      // The exported replaceTrack/resume API still enforces the guard. Some
      // exotic RTCRtpSender implementations are not extensible.
    }
  }

  return guard;
}
