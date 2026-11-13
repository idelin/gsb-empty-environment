import assert from 'node:assert/strict';
import { describe, it } from 'node:test';

import {
  WindowNameGuard,
  isSameBrowsingSite,
  transition,
} from './windowNameGuard.ts';

const httpsSite = (domain: string, path = '/') => ({
  scheme: 'https',
  registrableDomain: domain,
  path,
});

describe('WindowNameGuard pipeline state machine', () => {
  it('clears window.name when navigating to another registrable domain', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });
    guard.dispatch({ type: 'name-set', value: 'secret-token' });

    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('attacker.example'),
    });

    assert.equal(guard.windowName, '');
  });

  it('keeps window.name across same-site same-scheme path navigations', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example', '/login'),
    });
    guard.dispatch({ type: 'name-set', value: 'secret-token' });

    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example', '/dashboard'),
    });

    assert.equal(guard.windowName, 'secret-token');
  });

  it('clears window.name when the scheme changes on the same domain', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });
    guard.dispatch({ type: 'name-set', value: 'secret-token' });

    guard.dispatch({
      type: 'document-committed',
      document: { scheme: 'http', registrableDomain: 'bank.example', path: '/' },
    });

    assert.equal(guard.windowName, '');
  });

  it('clears window.name on the first committed document', () => {
    const guard = new WindowNameGuard();

    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });

    assert.equal(guard.windowName, '');
  });

  it('restores retention when navigating back to the original site after a cross-site trip', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });
    guard.dispatch({ type: 'name-set', value: 'secret-token' });
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('attacker.example'),
    });
    assert.equal(guard.windowName, '');

    guard.dispatch({ type: 'name-set', value: 'attacker-value' });
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });

    assert.equal(guard.windowName, '');
    assert.deepEqual(guard.currentDocument, httpsSite('bank.example'));
  });

  it('keeps the name on same-site navigation but clears it on cross-site navigation in one sequence', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });
    guard.dispatch({ type: 'name-set', value: 'token' });
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example', '/account'),
    });
    assert.equal(guard.windowName, 'token');

    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('shop.example'),
    });
    assert.equal(guard.windowName, '');
  });

  it('treats domain and scheme comparison as case-insensitive', () => {
    assert.equal(
      isSameBrowsingSite(
        { scheme: 'HTTPS', registrableDomain: 'Bank.Example', path: '/a' },
        httpsSite('bank.example', '/b'),
      ),
      true,
    );
    assert.equal(
      isSameBrowsingSite(
        httpsSite('bank.example'),
        httpsSite('other.example'),
      ),
      false,
    );
  });

  it('ignores name-set events before any document commits', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({ type: 'name-set', value: 'token' });

    assert.equal(guard.windowName, '');
    assert.equal(guard.currentDocument, null);
  });

  it('coerces name-set values to strings like the window.name property', () => {
    const guard = new WindowNameGuard();
    guard.dispatch({
      type: 'document-committed',
      document: httpsSite('bank.example'),
    });
    guard.dispatch({ type: 'name-set', value: 1234 as unknown as string });

    assert.equal(guard.windowName, '1234');
  });

  it('supports pure reducer usage via transition()', () => {
    let state = transition(
      { phase: 'idle', name: '' },
      { type: 'document-committed', document: httpsSite('bank.example') },
    );
    state = transition(state, { type: 'name-set', value: 'token' });
    state = transition(state, {
      type: 'document-committed',
      document: httpsSite('bank.example', '/next'),
    });
    assert.equal(state.phase === 'active' && state.name, 'token');

    state = transition(state, {
      type: 'document-committed',
      document: httpsSite('evil.example'),
    });
    assert.equal(state.name, '');
  });
});
