import { ActivationTracker } from '../src/activation.ts';
import { BrowserEnvironment } from '../src/environment.ts';
import { ManualClock } from '../src/clock.ts';
import { PausablePermissioner } from '../src/permissioner.ts';
import { RecordingSystemClipboard } from '../src/clipboard.ts';
import { ClipboardGuard } from '../src/guard.ts';

export interface Harness {
  clock: ManualClock;
  environment: BrowserEnvironment;
  activations: ActivationTracker;
  permissioner: PausablePermissioner;
  clipboard: RecordingSystemClipboard;
  guard: ClipboardGuard;
  /** 推进时钟并让出微任务，确保挂起的 Promise 回调完成。 */
  settle(advanceMs?: number): Promise<void>;
  flushMicrotasks(): Promise<void>;
}

export function createHarness(options: {
  activationTtlMs?: number;
  initialClipboard?: string;
  paused?: boolean;
} = {}): Harness {
  const clock = new ManualClock(1000);
  const environment = new BrowserEnvironment();
  const activations = new ActivationTracker(
    clock,
    options.activationTtlMs ?? 100,
  );
  const permissioner = new PausablePermissioner(clock);
  if (options.paused === false) {
    permissioner.resume();
  }
  const clipboard = new RecordingSystemClipboard(options.initialClipboard ?? '');
  const guard = new ClipboardGuard({
    clock,
    environment,
    activations,
    permissioner,
    clipboard,
  });

  const flushMicrotasks = (): Promise<void> =>
    new Promise((resolve) => setImmediate(resolve));

  return {
    clock,
    environment,
    activations,
    permissioner,
    clipboard,
    guard,
    settle: async (advanceMs?: number) => {
      if (advanceMs !== undefined) {
        clock.advance(advanceMs);
      }
      await flushMicrotasks();
    },
    flushMicrotasks,
  };
}
