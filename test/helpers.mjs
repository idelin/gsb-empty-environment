import assert from 'node:assert/strict';

const tests = [];

export function test(name, fn) {
  tests.push({ name, fn });
}

export function getTests() {
  return tests;
}

export { assert };
