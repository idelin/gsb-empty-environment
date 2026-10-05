import type { Message, Replica } from "./replica.ts";

/** One-directional anti-entropy: push all of `from`'s versions to `to`. */
export function gossip<V>(from: Replica<V>, to: Replica<V>): Message<V>[] {
  const messages = from.emit();
  for (const message of messages) to.receive(message);
  return messages;
}

/** Full bidirectional exchange between two replicas. */
export function sync<V>(a: Replica<V>, b: Replica<V>): void {
  gossip(a, b);
  gossip(b, a);
}

/** Sync every pair until no pair exchange changes anything (small clusters). */
export function syncAll<V>(replicas: Replica<V>[]): void {
  for (let round = 0; round < replicas.length; round++) {
    for (let i = 0; i < replicas.length; i++) {
      for (let j = i + 1; j < replicas.length; j++) {
        sync(replicas[i], replicas[j]);
      }
    }
  }
}
