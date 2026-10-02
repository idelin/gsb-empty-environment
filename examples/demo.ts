import { QuotaGuard, RuleSandbox, SandboxError } from '../src/index.ts';

const sandbox = new RuleSandbox(); // 1000-step budget, 1MB memory, 100ms timeout
const guard = new QuotaGuard({ maxEvaluationsPerWindow: 5, windowMs: 60_000 });

function show(tenant: string, source: string, variables: Record<string, string | number | boolean> = {}): void {
  const started = performance.now();
  try {
    const value = guard.execute(tenant, sandbox, source, variables);
    console.log(`[${tenant}] ${source}\n  => ${JSON.stringify(value)}  (${(performance.now() - started).toFixed(1)}ms)`);
  } catch (err) {
    if (err instanceof SandboxError) {
      console.log(`[${tenant}] ${source}\n  !! ${err.name}: ${err.message}  (${(performance.now() - started).toFixed(1)}ms)`);
    } else {
      throw err;
    }
  }
}

// Legitimate tenant rules
show('acme', 'balance > limit && tier == "gold"', { balance: 120, limit: 100, tier: 'gold' });
show('acme', 'round(price * qty * (1 - discount) * 100) / 100', { price: 19.99, qty: 3, discount: 0.1 });
show('acme', 'concat("user-", "42")');

// Hostile inputs
show('mallory', 'process.exit(1)');        // unauthorized function -> rejected before running
show('mallory', 'globalThis.constructor'); // member access -> unparseable
show('mallory', 'repeat("x", 99999999)');  // memory bomb -> MemoryExceeded
show('mallory', 'eval(self)');             // infinite recursion -> BudgetExceeded at 1000 steps

console.log('\nHost process is alive and responsive after all attacks.');
