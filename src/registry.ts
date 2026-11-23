import { DuplicateRegistrationError, InvalidDefinitionError, UnregisteredChannelError } from './errors.js';
import type { Schema } from './schema.js';

export interface ChannelDefinition {
  /** IPC channel name; must be a non-empty string without prototype keys. */
  channel: string;
  /** Method name exposed on the contextBridge surface. */
  exposedName: string;
  /** Schema applied to the cloned argument in the main process. */
  schema: Schema;
}

export interface RegisteredChannel {
  readonly channel: string;
  readonly exposedName: string;
  readonly schema: Schema;
}

const VALID_NAME = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const RESERVED = new Set(['__proto__', 'constructor', 'prototype']);

export class ChannelRegistry {
  private readonly channels = new Map<string, RegisteredChannel>();
  private readonly exposedNames = new Set<string>();

  constructor(definitions: readonly ChannelDefinition[] = []) {
    for (const definition of definitions) {
      this.register(definition);
    }
  }

  register(definition: ChannelDefinition): RegisteredChannel {
    const { channel, exposedName, schema } = definition;

    if (typeof channel !== 'string' || channel.length === 0) {
      throw new InvalidDefinitionError('channel must be a non-empty string');
    }
    if (RESERVED.has(channel) || channel.includes('\0')) {
      throw new InvalidDefinitionError(`invalid channel name: ${channel}`);
    }
    if (typeof exposedName !== 'string' || !VALID_NAME.test(exposedName) || RESERVED.has(exposedName)) {
      throw new InvalidDefinitionError(`invalid exposed method name: ${exposedName}`);
    }
    if (schema === null || typeof schema !== 'object' || typeof schema.type !== 'string') {
      throw new InvalidDefinitionError(`invalid schema for channel: ${channel}`);
    }

    if (this.channels.has(channel)) {
      throw new DuplicateRegistrationError(`channel already registered: ${channel}`);
    }
    if (this.exposedNames.has(exposedName)) {
      throw new DuplicateRegistrationError(`exposed method name already registered: ${exposedName}`);
    }

    const registered: RegisteredChannel = Object.freeze({ channel, exposedName, schema });
    this.channels.set(channel, registered);
    this.exposedNames.add(exposedName);
    return registered;
  }

  get(channel: string): RegisteredChannel {
    const entry = this.channels.get(channel);
    if (entry === undefined) {
      throw new UnregisteredChannelError(channel);
    }
    return entry;
  }

  has(channel: string): boolean {
    return this.channels.has(channel);
  }

  list(): readonly RegisteredChannel[] {
    return [...this.channels.values()];
  }
}
