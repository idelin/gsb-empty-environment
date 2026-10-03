import { test } from 'node:test';
import assert from 'node:assert/strict';
import { renderSql, renderScript } from '../src/sql-renderer.ts';

test('UPDATE 渲染：标识符加引号、字符串转义、NULL 字面量', () => {
  const sql = renderSql({
    kind: 'UPDATE',
    table: 'users',
    set: { name: "o'neil", balance: 0, deleted_at: null },
    where: { id: 7 },
    sourceSequence: 1,
  });
  assert.equal(
    sql,
    `UPDATE "users" SET "name" = 'o''neil', "balance" = 0, "deleted_at" = NULL WHERE "id" = 7;`,
  );
});

test('INSERT / DELETE 渲染', () => {
  assert.equal(
    renderSql({ kind: 'INSERT', table: 'users', values: { id: 1, vip: true }, sourceSequence: 1 }),
    `INSERT INTO "users" ("id", "vip") VALUES (1, TRUE);`,
  );
  assert.equal(
    renderSql({ kind: 'DELETE', table: 'users', where: { id: 1 }, sourceSequence: 1 }),
    `DELETE FROM "users" WHERE "id" = 1;`,
  );
});

test('脚本包裹在事务中', () => {
  const script = renderScript([
    { kind: 'DELETE', table: 'users', where: { id: 1 }, sourceSequence: 1 },
  ]);
  assert.match(script, /^BEGIN;\n/);
  assert.match(script, /COMMIT;\n$/);
});
