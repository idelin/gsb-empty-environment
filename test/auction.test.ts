import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  MAX_BIDS,
  AuctionValidationError,
  solveAuction,
  validateBids,
} from "../src/index.ts";
import type { Bid } from "../src/index.ts";

const bid = (id: string, bidder: string, items: string[], price: number): Bid => ({
  id,
  bidder,
  items,
  price,
});

describe("solveAuction: winner determination", () => {
  it("returns zero revenue and no winners for empty input", () => {
    const result = solveAuction([]);
    assert.equal(result.revenue, 0);
    assert.deepEqual(result.winners, []);
    assert.deepEqual(result.settlements, []);
  });

  it("picks the higher-priced bid when bundles overlap", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a", "b"], 10),
      bid("B2", "bob", ["b", "c"], 12),
    ]);
    assert.equal(result.revenue, 12);
    assert.deepEqual(result.winners.map((w) => w.id), ["B2"]);
  });

  it("combines disjoint bids when they beat a single bundle", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a", "b"], 10),
      bid("B2", "bob", ["a"], 6),
      bid("B3", "carol", ["b"], 6),
    ]);
    assert.equal(result.revenue, 12);
    assert.deepEqual(result.winners.map((w) => w.id), ["B2", "B3"]);
  });

  it("prefers a bundle when it beats the sum of singles", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a", "b"], 13),
      bid("B2", "bob", ["a"], 6),
      bid("B3", "carol", ["b"], 6),
    ]);
    assert.equal(result.revenue, 13);
    assert.deepEqual(result.winners.map((w) => w.id), ["B1"]);
  });

  it("treats bids from the same bidder as mutually exclusive", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a"], 10),
      bid("B2", "alice", ["a", "b"], 15),
      bid("B3", "bob", ["b"], 8),
    ]);
    // B2 (15) loses to B1 + B3 (18); B1 and B2 can never both win.
    assert.equal(result.revenue, 18);
    assert.deepEqual(result.winners.map((w) => w.id), ["B1", "B3"]);
  });

  it("breaks revenue ties by lexicographic order of winning bid ids", () => {
    const result = solveAuction([
      bid("Z", "zoe", ["a", "b"], 10),
      bid("X", "xenia", ["a"], 5),
      bid("Y", "yuri", ["b"], 5),
    ]);
    // Both {Z} and {X,Y} give revenue 10; ["X","Y"] < ["Z"] lexicographically.
    assert.equal(result.revenue, 10);
    assert.deepEqual(result.winners.map((w) => w.id), ["X", "Y"]);
  });

  it("handles a 25-bid instance quickly", () => {
    const bids: Bid[] = [];
    for (let i = 1; i <= MAX_BIDS; i += 1) {
      // 链式重叠：报价 i 占用物品 i 与 i+1。
      bids.push(bid(`B${i}`, `bidder${i}`, [`item${i}`, `item${i + 1}`], i));
    }
    const start = Date.now();
    const result = solveAuction(bids);
    assert.ok(Date.now() - start < 1000, "solver should finish within 1s");
    // 奇数报价互不重叠：1+3+...+25 = 169。
    assert.equal(result.revenue, 169);
    assert.equal(result.winners.length, 13);
  });
});

describe("solveAuction: settlement (VCG)", () => {
  it("computes revenue without each winner and the VCG payment", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a"], 5),
      bid("B2", "bob", ["b"], 5),
      bid("B3", "carol", ["a", "b"], 9),
    ]);
    assert.equal(result.revenue, 10);
    assert.deepEqual(result.winners.map((w) => w.id), ["B1", "B2"]);
    const [s1, s2] = result.settlements;
    // 移除 alice 后最优为 carol 的 9；支付 = 9 - (10 - 5) = 4。
    assert.equal(s1?.revenueWithoutWinner, 9);
    assert.equal(s1?.vcgPayment, 4);
    assert.equal(s2?.revenueWithoutWinner, 9);
    assert.equal(s2?.vcgPayment, 4);
  });

  it("removes all bids of the winner's bidder when computing externality", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a"], 10),
      bid("B2", "alice", ["a", "b"], 20),
      bid("B3", "bob", ["b"], 8),
    ]);
    assert.equal(result.revenue, 20);
    const [s] = result.settlements;
    // 移除 alice 的全部报价后，只剩 bob 的 8。
    assert.equal(s?.revenueWithoutWinner, 8);
    assert.equal(s?.vcgPayment, 8);
  });

  it("reports zero externality when a winner is not pivotal", () => {
    const result = solveAuction([
      bid("B1", "alice", ["a"], 5),
      bid("B2", "bob", ["a"], 4),
    ]);
    const [s] = result.settlements;
    assert.equal(s?.revenueWithoutWinner, 4);
    assert.equal(s?.vcgPayment, 4);
  });
});

describe("validateBids", () => {
  it("rejects duplicate items within a bundle", () => {
    assert.throws(
      () => validateBids([bid("B1", "alice", ["a", "b", "a"], 10)]),
      (err: unknown) =>
        err instanceof AuctionValidationError && /duplicate item/.test(err.message),
    );
  });

  it("rejects an empty bundle", () => {
    assert.throws(
      () => validateBids([bid("B1", "alice", [], 10)]),
      (err: unknown) =>
        err instanceof AuctionValidationError && /non-empty array/.test(err.message),
    );
  });

  it("rejects more than 25 bids", () => {
    const bids = Array.from({ length: MAX_BIDS + 1 }, (_, i) =>
      bid(`B${i}`, `bidder${i}`, [`item${i}`], 1),
    );
    assert.throws(
      () => validateBids(bids),
      (err: unknown) =>
        err instanceof AuctionValidationError && /too many bids/.test(err.message),
    );
  });

  it("rejects duplicate bid ids", () => {
    assert.throws(
      () =>
        validateBids([
          bid("B1", "alice", ["a"], 1),
          bid("B1", "bob", ["b"], 2),
        ]),
      (err: unknown) =>
        err instanceof AuctionValidationError && /duplicate bid id/.test(err.message),
    );
  });

  it("rejects negative or non-finite prices", () => {
    for (const price of [-1, Number.NaN, Number.POSITIVE_INFINITY]) {
      assert.throws(
        () => validateBids([bid("B1", "alice", ["a"], price)]),
        (err: unknown) =>
          err instanceof AuctionValidationError && /price/.test(err.message),
      );
    }
  });

  it("solveAuction surfaces validation errors", () => {
    assert.throws(
      () => solveAuction([bid("B1", "alice", ["a", "a"], 5)]),
      AuctionValidationError,
    );
  });
});
