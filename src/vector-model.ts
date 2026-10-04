import { contentHash, idKey, streamKey } from "./types.ts";
import type { Message, MessageId } from "./types.ts";

export class ModelConflictError extends Error {
  constructor(key: string) {
    super(`model: conflicting content for ${key}`);
    this.name = "ModelConflictError";
  }
}

export function indexMessages(messages: readonly Message[]): Map<string, Message> {
  const byKey = new Map<string, Message>();
  const hashes = new Map<string, string>();
  for (const message of messages) {
    const key = idKey(message.id);
    const hash = contentHash(message);
    const prior = hashes.get(key);
    if (prior !== undefined) {
      if (prior !== hash) throw new ModelConflictError(key);
      continue;
    }
    hashes.set(key, hash);
    byKey.set(key, message);
  }
  return byKey;
}

export function deliverableClosure(messages: readonly Message[]): Set<string> {
  const byKey = indexMessages(messages);
  const prefix = new Map<string, number>();
  const delivered = new Set<string>();
  let progressed = true;
  while (progressed) {
    progressed = false;
    for (const [key, message] of byKey) {
      if (delivered.has(key)) continue;
      const stream = streamKey(message.id.node, message.id.epoch);
      if (message.id.counter !== (prefix.get(stream) ?? 0) + 1) continue;
      let ready = true;
      for (const [depStream, need] of Object.entries(message.deps)) {
        if ((prefix.get(depStream) ?? 0) < need) {
          ready = false;
          break;
        }
      }
      if (!ready) continue;
      prefix.set(stream, message.id.counter);
      delivered.add(key);
      progressed = true;
    }
  }
  return delivered;
}

export function validateSequence(
  messages: readonly Message[],
  sequence: readonly MessageId[],
): string | null {
  const byKey = indexMessages(messages);
  const prefix = new Map<string, number>();
  const seen = new Set<string>();
  for (const id of sequence) {
    const key = idKey(id);
    if (seen.has(key)) return `duplicate delivery of ${key}`;
    const message = byKey.get(key);
    if (!message) return `delivery of unknown message ${key}`;
    const stream = streamKey(id.node, id.epoch);
    const expected = (prefix.get(stream) ?? 0) + 1;
    if (id.counter !== expected) {
      return `gap false-advance on ${stream}: delivered ${id.counter}, expected ${expected}`;
    }
    for (const [depStream, need] of Object.entries(message.deps)) {
      const have = prefix.get(depStream) ?? 0;
      if (have < need) {
        return `dependency violation: ${key} needs ${depStream} >= ${need}, have ${have}`;
      }
    }
    prefix.set(stream, id.counter);
    seen.add(key);
  }
  return null;
}
