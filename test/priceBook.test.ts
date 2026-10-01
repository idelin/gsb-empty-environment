import { test } from "node:test";
import assert from "node:assert/strict";
import { PriceBook } from "../src/priceBook.ts";

/** 构造验收场景：价格10 业务[0,20) 知悉于2；价格15 业务[5,10) 知悉于8 */
function makeAcceptanceBook(): PriceBook {
  const book = new PriceBook();
  book.addRule({ priceCents: 10, validFrom: 0, validTo: 20, txFrom: 2 });
  book.addRule({ priceCents: 15, validFrom: 5, validTo: 10, txFrom: 8 });
  return book;
}

test("验收边界：业务7 知悉7 -> 10（后一条规则尚不知悉）", () => {
  assert.equal(makeAcceptanceBook().priceAt(7, 7), 10);
});

test("验收边界：业务7 知悉8 -> 15（重叠时后知悉者优先）", () => {
  assert.equal(makeAcceptanceBook().priceAt(7, 8), 15);
});

test("验收边界：业务10 知悉8 -> 10（[5,10) 右开，不含10）", () => {
  assert.equal(makeAcceptanceBook().priceAt(10, 8), 10);
});

test("左闭右开：业务0 命中，业务20 不命中", () => {
  const book = makeAcceptanceBook();
  assert.equal(book.priceAt(0, 100), 10);
  assert.equal(book.priceAt(20, 100), null);
  assert.equal(book.priceAt(19, 100), 10);
});

test("知悉时间左闭：知悉2 可见，知悉1 不可见", () => {
  const book = makeAcceptanceBook();
  assert.equal(book.priceAt(0, 1), null);
  assert.equal(book.priceAt(0, 2), 10);
});

test("撤销只影响撤销之后的知悉时间查询", () => {
  const book = makeAcceptanceBook();
  // 知悉时刻12 撤销"价格15"规则
  const rules = book.snapshot();
  book.retract(rules[1]!.id, 12);

  // 撤销之前（知悉8/11）：仍得15
  assert.equal(book.priceAt(7, 8), 15);
  assert.equal(book.priceAt(7, 11), 15);
  // 撤销时刻起（知悉12）：回落到价格10
  assert.equal(book.priceAt(7, 12), 10);
  assert.equal(book.priceAt(7, 100), 10);
});

test("撤销唯一规则后，历史查询仍可见、之后查询为空", () => {
  const book = new PriceBook();
  const id = book.addRule({ priceCents: 99, validFrom: 0, validTo: 5, txFrom: 3 });
  book.retract(id, 6);

  assert.equal(book.priceAt(2, 5), 99); // 知悉5 < 6，仍可见
  assert.equal(book.priceAt(2, 6), null); // 知悉6 >= 6，已撤销
});

test("重复撤销与不存在的规则抛错", () => {
  const book = new PriceBook();
  const id = book.addRule({ priceCents: 1, validFrom: 0, validTo: 1, txFrom: 0 });
  book.retract(id, 5);
  assert.throws(() => book.retract(id, 6), /已被撤销/);
  assert.throws(() => book.retract(999, 6), /不存在/);
});

test("金额必须为整数分，业务区间必须非空", () => {
  const book = new PriceBook();
  assert.throws(
    () => book.addRule({ priceCents: 10.5, validFrom: 0, validTo: 1, txFrom: 0 }),
    /整数分/,
  );
  assert.throws(
    () => book.addRule({ priceCents: 10, validFrom: 5, validTo: 5, txFrom: 0 }),
    /左闭右开/,
  );
});

test("知悉时刻相同则后登记者优先", () => {
  const book = new PriceBook();
  book.addRule({ priceCents: 10, validFrom: 0, validTo: 10, txFrom: 2 });
  book.addRule({ priceCents: 20, validFrom: 0, validTo: 10, txFrom: 2 });
  assert.equal(book.priceAt(5, 2), 20);
});
