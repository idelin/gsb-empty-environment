import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  BrowsingContextGroupMachine,
  coopMatchesForNavigation,
  effectiveCoop,
  effectiveOrigin,
  matchPopupCoop,
  parseCoop,
  parseSandbox,
  resetBcgSequenceForTests,
} from '../src/coop.ts';
import { parseOrigin } from '../src/origin.ts';

const VICTIM = 'https://victim.example';
const ATTACKER = 'https://attacker.test';

function coop(value: string | null) {
  return parseCoop({ 'cross-origin-opener-policy': value });
}

test('missing/unknown COOP parses to unsafe-none', () => {
  assert.equal(parseCoop({}).value, 'unsafe-none');
  assert.equal(
    parseCoop({ 'cross-origin-opener-policy': 'bogus-directive' }).value,
    'unsafe-none',
  );
});

test('same enum same origin matches; same enum cross origin does not', () => {
  for (const value of ['same-origin', 'same-origin-allow-popups'] as const) {
    assert.equal(
      coopMatchesForNavigation(
        coop(value),
        coop(value),
        parseOrigin(VICTIM),
        parseOrigin(VICTIM),
      ),
      true,
    );
    assert.equal(
      coopMatchesForNavigation(
        coop(value),
        coop(value),
        parseOrigin(VICTIM),
        parseOrigin(ATTACKER),
      ),
      false,
    );
  }
  assert.equal(
    coopMatchesForNavigation(
      coop('unsafe-none'),
      coop('unsafe-none'),
      parseOrigin(VICTIM),
      parseOrigin(ATTACKER),
    ),
    true,
  );
});

test('BCG machine switches group on restrictive cross-origin navigation', () => {
  resetBcgSequenceForTests();
  const machine = new BrowsingContextGroupMachine({
    origin: VICTIM,
    coop: coop('same-origin'),
  });
  const initial = machine.state.browsingContextGroupId;
  const cross = machine.navigate({
    responseOrigin: ATTACKER,
    coop: coop('same-origin'),
  });
  assert.equal(cross.switchesBrowsingContextGroup, true);
  assert.equal(cross.reason, 'coop-cross-origin-restrictive');
  assert.notEqual(machine.state.browsingContextGroupId, initial);

  const back = machine.navigate({
    responseOrigin: ATTACKER,
    coop: coop('same-origin'),
  });
  assert.equal(back.switchesBrowsingContextGroup, false);
});

test('unsafe-none navigations stay in the same BCG', () => {
  resetBcgSequenceForTests();
  const machine = new BrowsingContextGroupMachine({ origin: VICTIM });
  const initial = machine.state.browsingContextGroupId;
  const decision = machine.navigate({
    responseOrigin: ATTACKER,
    coop: coop('unsafe-none'),
  });
  assert.equal(decision.switchesBrowsingContextGroup, false);
  assert.equal(machine.state.browsingContextGroupId, initial);
});

test('enum mismatch forces a BCG switch', () => {
  const machine = new BrowsingContextGroupMachine({
    origin: VICTIM,
    coop: coop('same-origin'),
  });
  const decision = machine.navigate({
    responseOrigin: VICTIM,
    coop: coop('same-origin-allow-popups'),
  });
  assert.equal(decision.switchesBrowsingContextGroup, true);
  assert.equal(decision.reason, 'coop-mismatch');
});

test('popup virtual policy: allow-popups retains opener for unsafe-none popup', () => {
  const retained = matchPopupCoop(
    coop('same-origin-allow-popups'),
    coop('unsafe-none'),
    parseOrigin(VICTIM),
    parseOrigin(ATTACKER),
  );
  assert.deepEqual(retained, { matches: true, openerRetained: true });

  const severed = matchPopupCoop(
    coop('same-origin'),
    coop('unsafe-none'),
    parseOrigin(VICTIM),
    parseOrigin(ATTACKER),
  );
  assert.deepEqual(severed, { matches: false, openerRetained: false });
});

test('openPopup tracks shared BCG and live opener link', () => {
  resetBcgSequenceForTests();
  const opener = new BrowsingContextGroupMachine({
    origin: VICTIM,
    coop: coop('same-origin-allow-popups'),
  });
  const { popup, decision } = opener.openPopup({
    responseOrigin: ATTACKER,
    coop: coop('unsafe-none'),
  });
  assert.equal(decision.openerRetained, true);
  assert.equal(
    popup.state.browsingContextGroupId,
    opener.state.browsingContextGroupId,
  );

  const strict = new BrowsingContextGroupMachine({
    origin: VICTIM,
    coop: coop('same-origin'),
  });
  const isolated = strict.openPopup({
    responseOrigin: ATTACKER,
    coop: coop('unsafe-none'),
  });
  assert.equal(isolated.decision.openerRetained, false);
  assert.notEqual(
    isolated.popup.state.browsingContextGroupId,
    strict.state.browsingContextGroupId,
  );
});

test('noopener always severs opener and allocates a fresh group', () => {
  const machine = new BrowsingContextGroupMachine({ origin: VICTIM });
  const { decision } = machine.openPopup({
    responseOrigin: VICTIM,
    noopener: true,
  });
  assert.equal(decision.switchesBrowsingContextGroup, true);
  assert.equal(decision.openerRetained, false);
  assert.equal(decision.reason, 'noopener');
});

test('sandbox without escape flag forces unsafe-none and opaque origin', () => {
  const sandbox = parseSandbox('allow-scripts allow-forms');
  assert.equal(sandbox.sandboxed, true);
  assert.equal(sandbox.allowPopupsToEscapeSandbox, false);
  assert.equal(
    effectiveCoop(coop('same-origin'), sandbox).value,
    'unsafe-none',
  );
  const origin = effectiveOrigin(VICTIM, sandbox);
  assert.equal(origin.opaque, true);

  const escape = parseSandbox(
    'allow-scripts allow-popups-to-escape-sandbox allow-same-origin',
  );
  assert.equal(effectiveCoop(coop('same-origin'), escape).value, 'same-origin');
  assert.equal(effectiveOrigin(VICTIM, escape).opaque, false);
});
