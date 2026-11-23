import { ipcMain } from 'electron';
import { registerBridgeHandlers } from '../src/index.js';
import { registry } from './registry.js';

registerBridgeHandlers(
  ipcMain,
  registry,
  {
    allowedOrigins: ['https://app.example.com'],
    requireMainFrame: true,
  },
  async (_event, channel, safeArg) => {
    // safeArg is a verified plain snapshot: functions, getters, Proxy traps
    // and prototype pollution keys have already been removed or rejected,
    // and the value has matched the registered schema.
    switch (channel) {
      case 'app:save-profile':
        return { saved: true, received: safeArg };
      case 'app:notify':
        return { notified: true };
      default:
        throw new Error(`unreachable: ${channel}`);
    }
  },
);
