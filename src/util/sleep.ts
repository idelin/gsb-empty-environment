import { setTimeout as delay } from 'node:timers/promises';

export function sleep(ms: number): Promise<void> {
  return delay(ms);
}
