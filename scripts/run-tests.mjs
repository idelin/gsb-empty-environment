// Runs the compiled test suite. On Node >= 18 the tests register with the
// built-in node:test runner; on older versions the harness fallback executes
// them. Equivalent to `node --test dist/test/` on Node >= 18.
import "../dist/test/arithmetic.test.js";
import { runFallback } from "../dist/test/harness.js";
await runFallback();
