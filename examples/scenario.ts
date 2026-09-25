/**
 * Demo scenario: differential access-control policies.
 *
 * v1 allows admins with clearance >= 5 (adults >= 18);
 * v2 tightened the rules: adults >= 21 and clearance >= 7.
 * The judgment difference is the disagreement between the two versions.
 */
import type { Schema } from '../src/schema.ts';
import type { Policy } from '../src/policy.ts';
import type { Input } from '../src/condition.ts';

export const accessSchema: Schema = {
  name: 'access-request',
  fields: {
    age: { type: { kind: 'int', min: 0, max: 120 }, required: true },
    roles: {
      type: { kind: 'set', of: ['admin', 'editor', 'viewer', 'auditor', 'guest'], minSize: 1 },
      required: true,
    },
    mfa: { type: { kind: 'bool' } },
    region: { type: { kind: 'enum', values: ['cn', 'us', 'eu'] } },
    // Dependent field: clearance exists iff the requester is an admin.
    clearance: {
      type: { kind: 'int', min: 0, max: 10 },
      presentWhen: { op: 'in', attr: 'roles', value: 'admin' },
    },
  },
  invariants: [
    // A guest can never hold any other role.
    {
      op: 'not',
      cond: {
        op: 'and',
        all: [
          { op: 'in', attr: 'roles', value: 'guest' },
          { op: 'or', any: [
            { op: 'in', attr: 'roles', value: 'admin' },
            { op: 'in', attr: 'roles', value: 'editor' },
          ] },
        ],
      },
    },
  ],
};

export const policyV1: Policy = {
  name: 'access',
  version: 'v1',
  rules: [
    { when: { op: 'cmp', attr: 'age', cmp: '<', value: 18 }, label: 'deny' },
    {
      when: { op: 'and', all: [
        { op: 'in', attr: 'roles', value: 'admin' },
        { op: 'cmp', attr: 'clearance', cmp: '>=', value: 5 },
      ] },
      label: 'allow',
    },
    { when: { op: 'in', attr: 'roles', value: 'editor' }, label: 'review' },
  ],
  default: 'deny',
};

export const policyV2: Policy = {
  name: 'access',
  version: 'v2',
  rules: [
    { when: { op: 'cmp', attr: 'age', cmp: '<', value: 21 }, label: 'deny' },
    {
      when: { op: 'and', all: [
        { op: 'in', attr: 'roles', value: 'admin' },
        { op: 'cmp', attr: 'clearance', cmp: '>=', value: 7 },
      ] },
      label: 'allow',
    },
    { when: { op: 'in', attr: 'roles', value: 'editor' }, label: 'review' },
  ],
  default: 'deny',
};

/** Failing input: v1 says "allow", v2 says "deny". */
export const failingInput: Input = {
  age: 35,
  roles: ['admin', 'editor', 'viewer'],
  mfa: true,
  region: 'us',
  clearance: 6,
};
