import { PartitionedBroadcastChannel, type ChannelOptions } from "./channel.ts";
import { DocumentRegistry, type DocumentToken } from "./document.ts";
import {
  storagePartition,
  type StoragePartition,
} from "./partition.ts";
import type { BroadcastRouter } from "./router.ts";
import { createBroadcastRouter } from "./router.ts";

export interface FrameContext {
  readonly docId: string;
  readonly document: DocumentToken;
  readonly origin: string;
  readonly topLevelSite: string;
  readonly partition: StoragePartition;
  createChannel<T>(name: string, options?: ChannelOptions): PartitionedBroadcastChannel<T>;
}

export interface NavigateOptions {
  origin?: string;
  topLevelSite?: string;
}

export class TestBrowserHost {
  readonly registry = new DocumentRegistry();
  readonly router: BroadcastRouter;

  constructor(router: BroadcastRouter = createBroadcastRouter()) {
    this.router = router;
  }

  openFrame(
    docId: string,
    origin: string,
    topLevelSite: string = origin,
  ): FrameContext {
    const document = this.registry.register(docId);
    const partition = storagePartition(origin, topLevelSite);
    const host = this;

    return {
      docId,
      document,
      origin,
      topLevelSite,
      partition,

      createChannel<T>(name: string, options: ChannelOptions = {}): PartitionedBroadcastChannel<T> {
        if (!host.registry.isLive(document)) {
          throw new Error(`document ${document.token} is no longer live`);
        }
        const channel = new PartitionedBroadcastChannel<T>(name, partition, {
          router: options.router ?? host.router,
        });
        host.registry.trackChannel(docId, channel);
        return channel;
      },
    };
  }

  navigate(frame: FrameContext, options: NavigateOptions = {}): FrameContext {
    this.registry.retire(frame.docId);
    return this.openFrame(
      frame.docId,
      options.origin ?? frame.origin,
      options.topLevelSite ?? frame.topLevelSite,
    );
  }

  closeFrame(frame: FrameContext): void {
    this.registry.retire(frame.docId);
  }

  flush(): Promise<void> {
    return this.router.flush();
  }

  get pendingCount(): number {
    return this.router.pendingCount;
  }

  get delivered() {
    return this.router.delivered;
  }
}
