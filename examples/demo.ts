/**
 * Runnable demo of the HLC engine.
 *
 *   node examples/demo.ts        (or: npm run demo)
 *
 * Simulates three physical-clock scenarios against a single HLCClock and
 * prints the emitted timestamps so the monotonicity guarantees are visible.
 */
import { HLCClock, pack, type Timestamp } from '../src/hlc.ts';

function format(ts: Timestamp): string {
  return `wall=${ts.wallTime} logical=${String(ts.logical).padStart(3)} packed=${pack(ts)}`;
}

// --- Scenario 1: physical clock stalls -------------------------------------
console.log('--- Scenario 1: physical clock stalls ---');
{
  let physical = 1_759_000_000_000;
  const hlc = new HLCClock({ physicalClock: () => physical });
  for (let i = 0; i < 5; i++) {
    console.log(' ', format(hlc.now()));
  }
  physical += 1; // clock resumes
  console.log(' ', format(hlc.now()), '<- physical clock resumed');
}

// --- Scenario 2: physical clock jumps backwards (NTP correction) ------------
console.log('--- Scenario 2: physical clock jumps backwards by 100ms ---');
{
  let physical = 1_759_000_000_000;
  const hlc = new HLCClock({ physicalClock: () => physical, epsilon: 500 });
  console.log(' ', format(hlc.now()));
  physical -= 100;
  for (let i = 0; i < 3; i++) {
    console.log(' ', format(hlc.now()), '<- monotonic despite the setback');
  }
}

// --- Scenario 3: physical clock leaps forward -------------------------------
console.log('--- Scenario 3: physical clock leaps forward by 60s ---');
{
  let physical = 1_759_000_000_000;
  const hlc = new HLCClock({ physicalClock: () => physical });
  hlc.now();
  console.log(' ', format(hlc.now()));
  physical += 60_000;
  console.log(' ', format(hlc.now()), '<- smooth catch-up, counter reset');
}

// --- Scenario 4: causal merge between two nodes -----------------------------
console.log('--- Scenario 4: message exchange between two nodes ---');
{
  let physicalA = 1_759_000_000_000;
  let physicalB = 1_759_000_000_050; // node B 50ms ahead
  const nodeA = new HLCClock({ physicalClock: () => physicalA });
  const nodeB = new HLCClock({ physicalClock: () => physicalB });

  const sent = nodeB.now();
  console.log(' B sends:   ', format(sent));
  const merged = nodeA.update(sent);
  console.log(' A receives:', format(merged), '<- causally after B');
  console.log(' A next:    ', format(nodeA.now()));
}
