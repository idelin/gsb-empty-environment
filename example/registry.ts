import { ChannelRegistry } from '../src/index.js';

/**
 * Single source of truth shared by the main process and the preload script.
 * Only channels registered here can ever receive a privileged handler.
 */
export const registry = new ChannelRegistry([
  {
    channel: 'app:save-profile',
    exposedName: 'saveProfile',
    schema: {
      type: 'object',
      properties: {
        name: { type: 'string' },
        tags: { type: 'array', items: { type: 'string' }, maxItems: 10 },
      },
      additionalProperties: false,
    },
  },
  {
    channel: 'app:notify',
    exposedName: 'notify',
    schema: {
      type: 'object',
      properties: {
        level: { type: 'string', enum: ['info', 'warn', 'error'] },
        message: { type: 'string' },
      },
      additionalProperties: false,
    },
  },
]);
