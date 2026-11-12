import type { PartitionedBroadcastChannel } from "./channel.ts";

export interface DocumentToken {
  readonly docId: string;
  readonly generation: number;
  readonly token: string;
}

export class DocumentRegistry {
  private generation = 0;
  private readonly live = new Map<string, { token: DocumentToken; channels: Set<PartitionedBroadcastChannel> }>();

  register(docId: string): DocumentToken {
    const existing = this.live.get(docId);
    if (existing) {
      throw new Error(`duplicate live document id: ${docId}`);
    }
    const generation = this.generation++;
    const token: DocumentToken = {
      docId,
      generation,
      token: `${docId}#${generation}`,
    };
    this.live.set(docId, { token, channels: new Set() });
    return token;
  }

  trackChannel(docId: string, channel: PartitionedBroadcastChannel): void {
    const entry = this.live.get(docId);
    if (!entry) {
      throw new Error(`unknown document id: ${docId}`);
    }
    entry.channels.add(channel);
  }

  retire(docId: string): DocumentToken | null {
    const entry = this.live.get(docId);
    if (!entry) {
      return null;
    }
    for (const channel of entry.channels) {
      channel.close();
    }
    this.live.delete(docId);
    return entry.token;
  }

  isLive(token: DocumentToken): boolean {
    return this.live.get(token.docId)?.token === token;
  }

  get liveCount(): number {
    return this.live.size;
  }
}
