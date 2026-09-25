/**
 * 示例：电商文档 v1 -> v4 版本图。
 *
 * 边：
 *   v1 -> v2 (cost 10): add_user_email
 *   v2 -> v3 (cost 10): add_tags_empty, add_tag_customer
 *   v3 -> v4 (cost 10): rename_updated_to_updatedAt
 *   v1 -> v4 (cost 25): 单步边“快速通道”，等价于上面三个版本的全部变更
 *
 * v1 -> v4：链路成本 30，捷径成本 25 => 确定性地选择捷径。
 */
import type { Schema } from '../src/schema.ts';
import { buildGraph } from '../src/graph.ts';
import { addField, appendUnique, renameField } from '../src/ops.ts';
import type { MigrationGraph } from '../src/graph.ts';

export const userV1: Schema = {
  type: 'object',
  required: ['id', 'name'],
  additionalProperties: false,
  properties: {
    id: { type: 'integer' },
    name: { type: 'string' },
  },
};

export const userV2: Schema = {
  type: 'object',
  required: ['id', 'name', 'email'],
  additionalProperties: false,
  properties: {
    id: { type: 'integer' },
    name: { type: 'string' },
    email: { type: 'string' },
  },
};

export const userV3: Schema = {
  type: 'object',
  required: ['id', 'name', 'email', 'tags'],
  additionalProperties: false,
  properties: {
    id: { type: 'integer' },
    name: { type: 'string' },
    email: { type: 'string' },
    tags: { type: 'array', items: { type: 'string' } },
  },
};

export const userV4: Schema = {
  type: 'object',
  required: ['id', 'name', 'email'],
  additionalProperties: false,
  properties: {
    id: { type: 'integer' },
    name: { type: 'string' },
    email: { type: 'string' },
  },
};

export const docV1: Schema = {
  type: 'object',
  required: ['version_marker', 'user', 'items'],
  additionalProperties: false,
  properties: {
    version_marker: { type: 'string' },
    user: userV1,
    items: { type: 'array', items: { type: 'object' } },
  },
};

export const docV2: Schema = {
  type: 'object',
  required: ['version_marker', 'user', 'items'],
  additionalProperties: false,
  properties: {
    version_marker: { type: 'string' },
    user: userV2,
    items: { type: 'array', items: { type: 'object' } },
  },
};

export const docV3TagsEmpty: Schema = {
  type: 'object',
  required: ['version_marker', 'user', 'items', 'tags', 'updated'],
  additionalProperties: false,
  properties: {
    version_marker: { type: 'string' },
    user: userV2,
    items: { type: 'array', items: { type: 'object' } },
    tags: { type: 'array', items: { type: 'string' }, minItems: 0, maxItems: 0 },
    updated: { type: 'string' },
  },
};

export const docV3: Schema = {
  type: 'object',
  required: ['version_marker', 'user', 'items', 'tags', 'updated'],
  additionalProperties: false,
  properties: {
    version_marker: { type: 'string' },
    user: userV2,
    items: { type: 'array', items: { type: 'object' } },
    tags: { type: 'array', items: { type: 'string' } },
    updated: { type: 'string' },
  },
};

export const docV4: Schema = {
  type: 'object',
  required: ['version_marker', 'user', 'items', 'tags', 'updatedAt'],
  additionalProperties: false,
  properties: {
    version_marker: { type: 'string' },
    user: userV4,
    items: { type: 'array', items: { type: 'object' } },
    tags: { type: 'array', items: { type: 'string' } },
    updatedAt: { type: 'string' },
  },
};

// ---- 可复用步骤工厂（捷径边与逐版本边共用，保证语义一致）----

export const addUserEmail = () => ({
  name: 'add_user_email',
  input: {
    type: 'object' as const,
    required: ['version_marker', 'user'],
    properties: {
      version_marker: { type: 'string' },
      user: userV1,
      items: { type: 'array', items: { type: 'object' } },
      tags: { type: 'array', items: { type: 'string' } },
      updatedAt: { type: 'string' },
    },
  } satisfies Schema,
  output: {
    type: 'object' as const,
    required: ['version_marker', 'user'],
    properties: {
      version_marker: { type: 'string' },
      user: userV2,
      items: { type: 'array', items: { type: 'object' } },
      tags: { type: 'array', items: { type: 'string' } },
      updatedAt: { type: 'string' },
    },
  } satisfies Schema,
  apply: (data: Record<string, unknown> & { user: { id: number; name: string } }) => ({
    ...data,
    user: addField(data.user, 'email', `user${data.user.id}@example.com`),
  }),
});

export const addTagsEmpty = () => ({
  name: 'add_tags_empty',
  input: {
    type: 'object' as const,
    required: ['version_marker', 'user'],
    properties: {
      version_marker: { type: 'string' },
      user: userV2,
      items: { type: 'array', items: { type: 'object' } },
    },
  } satisfies Schema,
  output: docV3TagsEmpty,
  apply: (data: Record<string, unknown>) => {
    const withTags = addField(data as never, 'tags', []);
    return addField(withTags, 'updated', '2026-09-25T00:00:00Z');
  },
});

export const addTagCustomer = () => ({
  name: 'add_tag_customer',
  input: docV3TagsEmpty,
  output: docV3,
  apply: (data: Record<string, unknown>) => appendUnique(data as never, 'tags', 'customer'),
});

export const renameUpdated = () => ({
  name: 'rename_updated_to_updatedAt',
  input: {
    type: 'object' as const,
    required: ['version_marker', 'user', 'tags', 'updated'],
    additionalProperties: false,
    properties: {
      version_marker: { type: 'string' },
      user: userV2,
      items: { type: 'array', items: { type: 'object' } },
      tags: { type: 'array', items: { type: 'string' } },
      updated: { type: 'string' },
    },
  } satisfies Schema,
  output: docV4,
  apply: (data: Record<string, unknown>) => renameField(data as never, 'updated', 'updatedAt'),
});

export function buildShopGraph(): MigrationGraph {
  return buildGraph([
    {
      from: 'v1',
      to: 'v2',
      priority: 10,
      steps: [addUserEmail()],
    },
    {
      from: 'v2',
      to: 'v3',
      priority: 10,
      steps: [addTagsEmpty(), addTagCustomer()],
    },
    {
      from: 'v3',
      to: 'v4',
      priority: 10,
      steps: [renameUpdated()],
    },
    {
      // 快速通道：成本 25 < 链路 30
      from: 'v1',
      to: 'v4',
      priority: 25,
      steps: [addUserEmail(), addTagsEmpty(), addTagCustomer(), renameUpdated()],
    },
  ]);
}
