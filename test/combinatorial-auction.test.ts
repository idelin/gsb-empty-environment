import test from "node:test";
import assert from "node:assert/strict";
import {
  AuctionInputError,
  MAX_BIDS,
  solveAuction,
  validateBids,
  type Bid,
} from "../src/combinatorial-auction.ts";

test("收入最大化：整包报价不敌分开报价", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a", "b"], price: 10 },
    { id: 2, bidder: "乙", items: ["a"], price: 6 },
    { id: 3, bidder: "丙", items: ["b"], price: 6 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 12);
  assert.deepEqual(result.winningBids.map((b) => b.id), [2, 3]);
});

test("同一投标方的多个报价互斥", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a"], price: 8 },
    { id: 2, bidder: "甲", items: ["a", "b"], price: 12 },
    { id: 3, bidder: "乙", items: ["b"], price: 5 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 13);
  assert.deepEqual(result.winningBids.map((b) => b.id), [1, 3]);
});

test("收入相同时按投标编号字典序选择", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a"], price: 5 },
    { id: 2, bidder: "乙", items: ["b"], price: 5 },
    { id: 3, bidder: "丙", items: ["a", "b"], price: 10 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 10);
  assert.deepEqual(result.winningBids.map((b) => b.id), [1, 2]);
});

test("字典序比较的是编号序列而非中标数量", () => {
  const bids: Bid[] = [
    { id: 5, bidder: "甲", items: ["a"], price: 5 },
    { id: 9, bidder: "乙", items: ["b"], price: 5 },
    { id: 3, bidder: "丙", items: ["a", "b"], price: 10 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 10);
  assert.deepEqual(result.winningBids.map((b) => b.id), [3]);
});

test("VCG 结算：移除每个中标者后重算最优收入", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a", "b"], price: 10 },
    { id: 2, bidder: "乙", items: ["a"], price: 6 },
    { id: 3, bidder: "丙", items: ["b"], price: 6 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 12);
  assert.equal(result.settlements.length, 2);
  for (const s of result.settlements) {
    assert.equal(s.revenueWithoutBidder, 10);
    assert.equal(s.othersRevenue, 6);
    assert.equal(s.vcgPayment, 4);
  }
});

test("移除中标者时其同方全部报价一并移除", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a"], price: 9 },
    { id: 2, bidder: "甲", items: ["a", "b"], price: 15 },
    { id: 3, bidder: "乙", items: ["b"], price: 8 },
  ];
  const result = solveAuction(bids);
  assert.equal(result.revenue, 17);
  assert.deepEqual(result.winningBids.map((b) => b.id), [1, 3]);
  const [first, second] = result.settlements;
  assert.equal(first.bid.id, 1);
  assert.equal(first.revenueWithoutBidder, 8);
  assert.equal(first.vcgPayment, 0);
  assert.equal(second.bid.id, 3);
  assert.equal(second.revenueWithoutBidder, 15);
  assert.equal(second.othersRevenue, 9);
  assert.equal(second.vcgPayment, 6);
});

test("空报价列表返回零收入空结果", () => {
  const result = solveAuction([]);
  assert.equal(result.revenue, 0);
  assert.deepEqual(result.winningBids, []);
  assert.deepEqual(result.settlements, []);
});

test("校验：同一报价内物品重复", () => {
  const bids: Bid[] = [{ id: 1, bidder: "甲", items: ["a", "a"], price: 5 }];
  assert.throws(() => validateBids(bids), AuctionInputError);
  assert.throws(() => solveAuction(bids), /重复物品/);
});

test("校验：物品组合为空", () => {
  const bids: Bid[] = [{ id: 1, bidder: "甲", items: [], price: 5 }];
  assert.throws(() => validateBids(bids), AuctionInputError);
  assert.throws(() => solveAuction(bids), /物品组合为空/);
});

test("校验：投标编号重复", () => {
  const bids: Bid[] = [
    { id: 1, bidder: "甲", items: ["a"], price: 5 },
    { id: 1, bidder: "乙", items: ["b"], price: 5 },
  ];
  assert.throws(() => solveAuction(bids), /重复/);
});

test("校验：报价数量超过上限", () => {
  const bids: Bid[] = Array.from({ length: MAX_BIDS + 1 }, (_, i) => ({
    id: i + 1,
    bidder: `投标方${i + 1}`,
    items: [`item${i + 1}`],
    price: 1,
  }));
  assert.throws(() => solveAuction(bids), /超过上限/);
});

test("校验：非法报价金额", () => {
  assert.throws(
    () => solveAuction([{ id: 1, bidder: "甲", items: ["a"], price: -1 }]),
    /非负/,
  );
  assert.throws(
    () => solveAuction([{ id: 1, bidder: "甲", items: ["a"], price: NaN }]),
    /非负/,
  );
});

test("25 个互不冲突的报价全部中标", () => {
  const bids: Bid[] = Array.from({ length: MAX_BIDS }, (_, i) => ({
    id: i + 1,
    bidder: `投标方${i + 1}`,
    items: [`item${i + 1}`],
    price: i + 1,
  }));
  const result = solveAuction(bids);
  assert.equal(result.revenue, (25 * 26) / 2);
  assert.equal(result.winningBids.length, MAX_BIDS);
  for (const s of result.settlements) {
    assert.equal(s.vcgPayment, 0);
  }
});

test("25 个链式冲突同价报价：最大独立集加字典序", () => {
  const bids: Bid[] = Array.from({ length: MAX_BIDS }, (_, i) => ({
    id: i + 1,
    bidder: `投标方${i + 1}`,
    items: [`edge${i + 1}`, `edge${i + 2}`],
    price: 10,
  }));
  const result = solveAuction(bids);
  assert.equal(result.revenue, 130);
  assert.deepEqual(
    result.winningBids.map((b) => b.id),
    [1, 3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25],
  );
});
