import type { SchemaInput, SecurityPolicyInput } from '../src/index.ts';

export const tenantSchema: SchemaInput = {
  queryType: 'Query',
  mutationType: 'Mutation',
  types: {
    Query: {
      kind: 'OBJECT',
      fields: {
        me: { type: 'User', weight: 2 },
        users: {
          type: '[User!]!',
          weight: 5,
          args: {
            first: { type: 'Int' },
            limit: { type: 'Int' },
            tenantId: { type: 'ID!', sensitive: true },
            filter: { type: 'UserFilterInput' },
          },
        },
        user: {
          type: 'User',
          args: { id: { type: 'ID!' } },
        },
        orders: {
          type: '[Order!]!',
          args: { first: { type: 'Int' }, tenantId: { type: 'ID!', sensitive: true } },
        },
        loginAttempt: {
          type: 'AuthResult',
          weight: 10,
          args: { email: { type: 'String!' }, password: { type: 'String!' } },
        },
      },
    },
    Mutation: {
      kind: 'OBJECT',
      fields: {
        updateUser: {
          type: 'User',
          args: {
            id: { type: 'ID!' },
            scope: { type: 'TenantScopeInput!' },
          },
        },
      },
    },
    User: {
      kind: 'OBJECT',
      interfaces: ['Node'],
      fields: {
        id: { type: 'ID!' },
        name: { type: 'String!' },
        email: { type: 'String' },
        tenantToken: { type: 'String!' },
        roles: { type: '[String!]!' },
        orders: {
          type: '[Order!]!',
          args: { first: { type: 'Int' } },
        },
      },
    },
    Node: {
      kind: 'INTERFACE',
      fields: { id: { type: 'ID!' } },
      possibleTypes: ['User'],
    },
    Order: {
      kind: 'OBJECT',
      fields: {
        id: { type: 'ID!' },
        total: { type: 'Float!' },
        secret: { type: 'String!' },
      },
    },
    AuthResult: {
      kind: 'OBJECT',
      fields: {
        token: { type: 'String' },
        success: { type: 'Boolean!' },
      },
    },
    UserFilterInput: {
      kind: 'INPUT_OBJECT',
      inputFields: {
        name: { type: 'String' },
        tenantId: { type: 'ID', sensitive: true },
      },
    },
    TenantScopeInput: {
      kind: 'INPUT_OBJECT',
      inputFields: {
        tenantId: { type: 'ID!', sensitive: true },
        region: { type: 'String' },
      },
    },
    String: { kind: 'SCALAR' },
    ID: { kind: 'SCALAR' },
    Int: { kind: 'SCALAR' },
    Float: { kind: 'SCALAR' },
    Boolean: { kind: 'SCALAR' },
    UserRole: { kind: 'ENUM', enumValues: ['ADMIN', 'VIEWER'] },
  },
};

export function policyFor(
  role: string,
  overrides: Partial<SecurityPolicyInput> = {},
): SecurityPolicyInput {
  return {
    role,
    maxDepth: 8,
    maxAliasMultiplier: 20,
    maxComplexity: 500,
    maxListSize: 50,
    blockIntrospection: true,
    defaultAllow: false,
    policies: {
      admin: {
        defaultAllow: true,
        fields: {
          'User.tenantToken': false,
          'Order.secret': false,
        },
      },
      viewer: {
        defaultAllow: false,
        fields: {
          'Query.me': true,
          'Query.users': true,
          'Query.user': true,
          'Query.orders': true,
          'User.id': true,
          'User.name': true,
          'User.email': true,
          'User.roles': true,
          'User.orders': true,
          'Order.id': true,
          'Order.total': true,
        },
      },
    },
    ...overrides,
  };
}
