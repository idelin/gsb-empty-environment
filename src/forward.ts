import { CAP_MESSAGE_TAGS, ConnectionCapState } from './cap.ts';
import type { IrcMessage } from './parser.ts';
import { serializeMessage } from './serialize.ts';
import { selectClientTags } from './tags.ts';
import type { TagMap } from './tags.ts';

export interface ForwardTarget {
  id: string;
  capState: ConnectionCapState;
  /** Invoked with the serialized line (without CRLF) for this recipient. */
  send: (line: string) => void;
}

export interface ForwardResult {
  targetId: string;
  /** Whether the recipient had ACKed message-tags and therefore got tags. */
  negotiated: boolean;
  /** The exact line handed to the recipient. */
  line: string;
}

function buildLine(message: IrcMessage, tags: TagMap | undefined): string {
  return serializeMessage({
    tags: tags && tags.size > 0 ? tags : undefined,
    prefix: message.prefix,
    command: message.command,
    params: message.params,
  });
}

/**
 * Build the wire line a recipient should receive. Only client-only tags
 * (leading `+`) are eligible for relay, and they are attached solely when the
 * recipient has ACKed the message-tags capability. Everyone else receives the
 * exact same message without any tag section.
 */
export function buildForwardLine(message: IrcMessage, target: ForwardTarget): string {
  const negotiated = target.capState.has(CAP_MESSAGE_TAGS);
  if (!negotiated || message.tags === undefined) {
    return buildLine(message, undefined);
  }
  return buildLine(message, selectClientTags(message.tags));
}

/**
 * Fan a message out to every target. Recipients without message-tags are
 * still delivered the tag-free message; nothing else about routing changes.
 */
export function forwardMessage(message: IrcMessage, targets: readonly ForwardTarget[]): ForwardResult[] {
  return targets.map((target) => {
    const line = buildForwardLine(message, target);
    target.send(line);
    return {
      targetId: target.id,
      negotiated: target.capState.has(CAP_MESSAGE_TAGS),
      line,
    };
  });
}
