import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  TrackGuardError,
  createSendTrackGuard,
  createShareConsent,
  type DisplaySurface,
  type SendTrackConsent,
  type TrackKind,
} from "./index.ts";

interface FakeTrackInit {
  id: string;
  kind: TrackKind;
  deviceId: string;
  displaySurface?: DisplaySurface;
  width?: number;
  height?: number;
}

class FakeMediaStreamTrack extends EventTarget {
  readonly id: string;
  readonly kind: TrackKind;
  readyState: MediaStreamTrackState = "live";
  enabled = true;
  settings: MediaTrackSettings;

  constructor(init: FakeTrackInit) {
    super();
    this.id = init.id;
    this.kind = init.kind;
    this.settings = {
      deviceId: init.deviceId,
      ...(init.displaySurface === undefined
        ? {}
        : { displaySurface: init.displaySurface }),
      ...(init.width === undefined ? {} : { width: init.width }),
      ...(init.height === undefined ? {} : { height: init.height }),
    };
  }

  getSettings(): MediaTrackSettings {
    return { ...this.settings };
  }

  changeSettings(next: Partial<MediaTrackSettings>): void {
    this.settings = { ...this.settings, ...next };
    this.dispatchEvent(new Event("settingschange"));
  }

  end(): void {
    this.readyState = "ended";
    this.dispatchEvent(new Event("ended"));
  }
}

class FakeRtpSender {
  track: FakeMediaStreamTrack | null;
  replaceCalls: Array<FakeMediaStreamTrack | null> = [];

  constructor(track: FakeMediaStreamTrack | null) {
    this.track = track;
  }

  async replaceTrack(track: FakeMediaStreamTrack | null): Promise<void> {
    this.replaceCalls.push(track);
    this.track = track;
  }
}

class DeferredRtpSender {
  track: FakeMediaStreamTrack | null;
  private release: (() => void) | null = null;

  constructor(track: FakeMediaStreamTrack | null) {
    this.track = track;
  }

  async replaceTrack(track: FakeMediaStreamTrack | null): Promise<void> {
    if (track === null) {
      this.track = null;
      return;
    }
    await new Promise<void>((resolve) => {
      this.release = resolve;
    });
    this.track = track;
  }

  complete(): void {
    this.release?.();
    this.release = null;
  }
}

function makeTrack(init: FakeTrackInit): FakeMediaStreamTrack {
  return new FakeMediaStreamTrack(init);
}

function makeWindowTrack(id = "window-track"): FakeMediaStreamTrack {
  return makeTrack({
    id,
    kind: "video",
    deviceId: "display-source-42",
    displaySurface: "window",
    width: 960,
    height: 540,
  });
}

function makeSender(
  track: FakeMediaStreamTrack | null,
): FakeRtpSender {
  return new FakeRtpSender(track);
}

function senderAsRtpSender(sender: FakeRtpSender): RTCRtpSender {
  return sender as unknown as RTCRtpSender;
}

function installGuard(
  track: FakeMediaStreamTrack,
  consent?: SendTrackConsent,
): {
  sender: FakeRtpSender;
  guard: ReturnType<typeof createSendTrackGuard>;
  consent: SendTrackConsent;
} {
  const sender = makeSender(track);
  const approved =
    consent ?? createShareConsent(track as unknown as MediaStreamTrack, {
      id: "consent-window-1",
    });
  const guard = createSendTrackGuard(senderAsRtpSender(sender), approved);
  return { sender, guard, consent: approved };
}

describe("createShareConsent", () => {
  it("captures and freezes kind, deviceId and displaySurface", () => {
    const track = makeWindowTrack();
    const consent = createShareConsent(track as unknown as MediaStreamTrack, {
      id: "consent-1",
    });

    assert.deepEqual(consent, {
      id: "consent-1",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "window",
    });
    assert.equal(Object.isFrozen(consent), true);
    assert.throws(() => {
      (consent as { deviceId: string }).deviceId = "attacker-source";
    }, TypeError);
  });

  it("records a camera without a displaySurface", () => {
    const camera = makeTrack({
      id: "camera-1",
      kind: "video",
      deviceId: "camera-device-1",
    });
    const consent = createShareConsent(camera as unknown as MediaStreamTrack, {
      id: "consent-camera",
    });

    assert.equal(consent.kind, "video");
    assert.equal(consent.deviceId, "camera-device-1");
    assert.equal(consent.displaySurface, undefined);
  });
});

describe("createSendTrackGuard", () => {
  it("refuses to swap an approved window for a full-screen monitor track", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);

    const fullScreenTrack = makeTrack({
      id: "fullscreen-track",
      kind: "video",
      // Reuse the approved deviceId to show that displaySurface is enforced
      // independently instead of being hidden behind a device mismatch.
      deviceId: "display-source-42",
      displaySurface: "monitor",
      width: 2560,
      height: 1440,
    });

    await assert.rejects(
      guard.replaceTrack(fullScreenTrack as unknown as MediaStreamTrack),
      (error: unknown) =>
        error instanceof TrackGuardError &&
        error.code === "display-surface-mismatch",
    );

    assert.equal(guard.paused, true);
    assert.equal(guard.pauseReason, "invalid-replacement");
    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);
    assert.equal(fullScreenTrack.enabled, true);
    assert.equal(sender.replaceCalls.includes(fullScreenTrack), false);
  });

  it("refuses to swap the window for a camera track", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);
    const cameraTrack = makeTrack({
      id: "camera-track",
      kind: "video",
      deviceId: "camera-device-1",
    });

    await assert.rejects(
      guard.replaceTrack(cameraTrack as unknown as MediaStreamTrack),
      (error: unknown) =>
        error instanceof TrackGuardError && error.code === "device-mismatch",
    );

    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);
    assert.equal(guard.lastViolation?.reason, "invalid-replacement");
  });

  it("guards direct sender.replaceTrack calls as well", async () => {
    const windowTrack = makeWindowTrack();
    const { sender } = installGuard(windowTrack);
    const fullScreenTrack = makeTrack({
      id: "fullscreen-direct",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "monitor",
    });

    await assert.rejects(
      (
        sender as unknown as {
          replaceTrack(track: MediaStreamTrack | null): Promise<void>;
        }
      ).replaceTrack(fullScreenTrack as unknown as MediaStreamTrack),
      (error: unknown) =>
        error instanceof TrackGuardError &&
        error.code === "display-surface-mismatch",
    );
    assert.equal(sender.track, null);
  });

  it("allows replacing with another track from the same approved window", async () => {
    const windowTrack = makeWindowTrack("window-1");
    const { sender, guard } = installGuard(windowTrack);
    const refreshedWindow = makeTrack({
      id: "window-2",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "window",
      width: 1280,
      height: 720,
    });

    await guard.replaceTrack(refreshedWindow as unknown as MediaStreamTrack);

    assert.equal(guard.paused, false);
    assert.equal(sender.track, refreshedWindow);
    assert.equal(guard.currentTrack, refreshedWindow);
  });

  it("pauses sending when an attached track emits settingschange", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);

    windowTrack.changeSettings({ displaySurface: "monitor" });

    assert.equal(guard.paused, true);
    assert.equal(guard.pauseReason, "settings-changed");
    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);
    assert.equal(guard.lastViolation?.error?.code, "display-surface-mismatch");

    await assert.rejects(guard.resume(), (error: unknown) => {
      return (
        error instanceof TrackGuardError &&
        error.code === "resume-failed"
      );
    });
    assert.equal(sender.track, null);
    assert.equal(guard.paused, true);
  });

  it("pauses on any settingschange, even before the tuple drifts", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);

    windowTrack.changeSettings({ width: 1280 });

    assert.equal(guard.paused, true);
    assert.equal(guard.pauseReason, "settings-changed");
    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);

    // The tuple is still valid, so an explicit review may resume.
    await guard.resume();
    assert.equal(guard.paused, false);
    assert.equal(sender.track, windowTrack);
  });

  it("pauses sending when the approved track ends", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);

    windowTrack.end();

    assert.equal(guard.paused, true);
    assert.equal(guard.pauseReason, "ready-state-abnormal");
    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);
  });

  it("refuses a replacement track whose readyState is not live", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);
    const endedWindow = makeTrack({
      id: "ended-window",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "window",
    });
    endedWindow.end();

    await assert.rejects(
      guard.replaceTrack(endedWindow as unknown as MediaStreamTrack),
      (error: unknown) =>
        error instanceof TrackGuardError && error.code === "track-not-live",
    );
    assert.equal(sender.track, null);
    assert.equal(guard.paused, true);
  });

  it("keeps the guard paused even if a valid track is offered after a violation", async () => {
    const windowTrack = makeWindowTrack();
    const { guard } = installGuard(windowTrack);
    const fullScreenTrack = makeTrack({
      id: "fullscreen-2",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "monitor",
    });

    await assert.rejects(
      guard.replaceTrack(fullScreenTrack as unknown as MediaStreamTrack),
    );

    const anotherWindow = makeTrack({
      id: "window-3",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "window",
    });
    await assert.rejects(
      guard.replaceTrack(anotherWindow as unknown as MediaStreamTrack),
      (error: unknown) =>
        error instanceof TrackGuardError && error.code === "guard-paused",
    );
  });

  it("resumes the last approved window after an invalid replacement attempt", async () => {
    const windowTrack = makeWindowTrack();
    const { sender, guard } = installGuard(windowTrack);
    const fullScreenTrack = makeTrack({
      id: "fullscreen-3",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "monitor",
    });

    await assert.rejects(
      guard.replaceTrack(fullScreenTrack as unknown as MediaStreamTrack),
    );
    await guard.resume();

    assert.equal(guard.paused, false);
    assert.equal(sender.track, windowTrack);
    assert.equal(windowTrack.enabled, true);
  });

  it("fails closed when a pause races an in-flight replacement", async () => {
    const windowTrack = makeWindowTrack();
    const sender = new DeferredRtpSender(windowTrack);
    const consent = createShareConsent(
      windowTrack as unknown as MediaStreamTrack,
      { id: "consent-race" },
    );
    const guard = createSendTrackGuard(
      sender as unknown as RTCRtpSender,
      consent,
    );

    const refreshedWindow = makeTrack({
      id: "window-race",
      kind: "video",
      deviceId: "display-source-42",
      displaySurface: "window",
    });

    const pendingReplace = guard.replaceTrack(
      refreshedWindow as unknown as MediaStreamTrack,
    );
    windowTrack.changeSettings({ width: 1280 });
    sender.complete();

    await assert.rejects(pendingReplace, (error: unknown) => {
      return (
        error instanceof TrackGuardError && error.code === "guard-paused"
      );
    });
    assert.equal(guard.paused, true);
    assert.equal(sender.track, null);
    assert.equal(windowTrack.enabled, false);
  });
});
