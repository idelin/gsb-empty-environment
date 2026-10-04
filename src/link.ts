import { LinkMessage } from './types';

/**
 * Unreliable link between A and B: messages may be delayed arbitrarily
 * (causing reordering) and redelivered (duplicates). Messages are never
 * dropped, matching the "out-of-order, re-sent deltas" requirement.
 */
export class UnreliableLink {
  sent = 0;
  duplicated = 0;

  constructor(
    private rand: () => number,
    private deliver: (at: number, msg: LinkMessage) => void,
    private maxDelay = 8,
  ) {}

  send(now: number, msg: LinkMessage): void {
    this.sent++;
    let copies = 1;
    if (this.rand() < 0.25) copies++;
    if (this.rand() < 0.05) copies++;
    this.duplicated += copies - 1;
    for (let i = 0; i < copies; i++) {
      const delay = 1 + Math.floor(this.rand() * this.maxDelay);
      this.deliver(now + delay, msg);
    }
  }
}
