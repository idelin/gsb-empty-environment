// Test entry point: imports every suite, then prints the summary.
import './fraction.test.js';
import './ccd.test.js';
import './world.test.js';
import './cli.test.js';
import { report } from './harness.js';

report();
