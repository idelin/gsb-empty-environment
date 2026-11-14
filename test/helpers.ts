import type { ContentWindowLike } from "../src/bridge.ts";
import type {
  RuntimeLike,
  RuntimeMessageSender,
} from "../src/content_script.ts";
import type {
  StructuralContainer,
  StructuralNode,
} from "../src/dom.ts";

export interface PostedMessage {
  readonly message: unknown;
  readonly targetOrigin: string;
  readonly ports: MessagePort[];
}

class FakeMessageEvent extends Event {
  readonly data: unknown;
  readonly origin: string;
  readonly source: unknown;
  readonly ports: MessagePort[];

  constructor(options: Readonly<{
    data: unknown;
    origin: string;
    source: unknown;
    ports: MessagePort[];
  }>) {
    super("message");
    this.data = options.data;
    this.origin = options.origin;
    this.source = options.source;
    this.ports = options.ports;
  }
}

export class FakeWindow extends EventTarget implements ContentWindowLike {
  readonly location: { origin: string };
  readonly posted: PostedMessage[] = [];

  constructor(origin: string) {
    super();
    this.location = { origin };
  }

  postMessage(
    message: unknown,
    targetOrigin: string,
    transfer: Transferable[] = [],
  ): void {
    this.posted.push({
      message,
      targetOrigin,
      ports: transfer.filter(
        (entry): entry is MessagePort => entry instanceof MessagePort,
      ),
    });
  }

  emitMessage(options: Readonly<{
    data: unknown;
    origin?: string;
    source?: unknown;
    ports?: MessagePort[];
  }>): void {
    const event = new FakeMessageEvent({
      data: options.data,
      origin: options.origin ?? this.location.origin,
      ports: options.ports ?? [],
      source: options.source === undefined ? this : options.source,
    });
    this.dispatchEvent(event);
  }
}

type RuntimeListener = (
  message: unknown,
  sender: RuntimeMessageSender,
  sendResponse: (response?: unknown) => void,
) => boolean | void;

export class FakeRuntime implements RuntimeLike {
  readonly id: string;
  readonly manifest: unknown;
  readonly listeners = new Set<RuntimeListener>();
  readonly onMessage = {
    addListener: (listener: RuntimeListener): void => {
      this.listeners.add(listener);
    },
    removeListener: (listener: RuntimeListener): void => {
      this.listeners.delete(listener);
    },
  };

  constructor(id: string, manifest: unknown) {
    this.id = id;
    this.manifest = manifest;
  }

  getManifest(): unknown {
    return this.manifest;
  }

  emit(message: unknown, sender: RuntimeMessageSender): unknown[] {
    const responses: unknown[] = [];
    for (const listener of this.listeners) {
      listener(message, sender, (response) => responses.push(response));
    }
    return responses;
  }
}

export class FakeElement implements StructuralNode {
  readonly localName: string;
  readonly children: FakeElement[];
  readonly id: string | undefined;
  readonly className: string | undefined;
  readonly name: string | undefined;

  constructor(
    localName: string,
    options: Readonly<{
      id?: string;
      className?: string;
      name?: string;
    }> = {},
  ) {
    this.localName = localName;
    this.children = [];
    this.id = options.id;
    this.className = options.className;
    this.name = options.name;
  }

  append(...nodes: FakeElement[]): this {
    this.children.push(...nodes);
    return this;
  }
}

export class FakeDocument implements StructuralContainer {
  readonly children: FakeElement[];

  constructor(...children: FakeElement[]) {
    this.children = children;
  }
}

export function buildDocument(mains: FakeElement[] = [new FakeElement("main")]) {
  const body = new FakeElement("body").append(...mains);
  const head = new FakeElement("head");
  const html = new FakeElement("html").append(head, body);
  return {
    document: new FakeDocument(html),
    html,
    body,
    mains,
  };
}

export function extensionManifest(
  origins: readonly string[] = ["https://api.example.com"],
  security: Readonly<Record<string, unknown>> = {},
) {
  return {
    manifest_version: 3,
    name: "test-extension",
    version: "1.0.0",
    host_permissions: origins.map((origin) => `${origin}/*`),
    extension_security: {
      version: 1,
      maxPageMessageBytes: 2_048,
      maxPageStringLength: 1_024,
      handshakeCooldownMs: 0,
      ...security,
    },
  };
}

export function waitForPortMessage(
  port: MessagePort,
  timeoutMs = 1_000,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      port.onmessage = null;
      reject(new Error("timed out waiting for port message"));
    }, timeoutMs);
    port.onmessage = (event): void => {
      clearTimeout(timer);
      port.onmessage = null;
      resolve(event.data);
    };
  });
}

export function delay(milliseconds: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, milliseconds);
  });
}

export function hello(
  requestId = "hello-1",
): Readonly<Record<string, unknown>> {
  return Object.freeze({
    type: "extension:bridge-hello",
    protocolVersion: 1,
    requestId,
  });
}
