import { report } from './harness';
import { runTargeted } from './targeted';
import { runExhaustive } from './exhaustive';

runTargeted();
runExhaustive();
report();
