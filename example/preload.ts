import { exposeBridge } from '../src/preload-electron.js';
import { registry } from './registry.js';

// The renderer only sees window.secureBridge with saveProfile and notify.
exposeBridge('secureBridge', registry);
