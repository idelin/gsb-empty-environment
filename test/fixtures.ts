import type { GraphVersion } from "../src/index.ts";

export const graphV1: GraphVersion = {
  version: 1,
  states: ["created", "paid", "shipped", "refund_pending", "refund_review", "closed"],
  initialState: "created",
  transitions: [
    { event: "pay", from: ["created"], to: "paid" },
    { event: "ship", from: ["paid"], to: "shipped" },
    { event: "request_refund", from: ["paid"], to: "refund_pending" },
    { event: "escalate_refund", from: ["refund_pending"], to: "refund_review" },
    { event: "refund", from: ["refund_pending", "refund_review"], to: "closed" },
    { event: "close", from: ["shipped"], to: "closed" },
  ],
};

export const graphV2: GraphVersion = {
  version: 2,
  states: ["created", "paid", "shipped", "refunding", "closed"],
  initialState: "created",
  transitions: [
    { event: "pay", from: ["created"], to: "paid" },
    { event: "ship", from: ["paid"], to: "shipped" },
    { event: "request_refund", from: ["paid"], to: "refunding" },
    { event: "cancel_refund", from: ["refunding"], to: "paid" },
    { event: "refund", from: ["refunding"], to: "closed" },
    { event: "close", from: ["shipped"], to: "closed" },
  ],
  stateMapping: {
    created: "created",
    paid: "paid",
    shipped: "shipped",
    refund_pending: "refunding",
    refund_review: "refunding",
    closed: "closed",
  },
};

export const graphV2Partial: GraphVersion = {
  version: 2,
  states: ["created", "paid", "refunding", "closed"],
  initialState: "created",
  transitions: [
    { event: "pay", from: ["created"], to: "paid" },
    { event: "request_refund", from: ["paid"], to: "refunding" },
    { event: "refund", from: ["refunding"], to: "closed" },
  ],
  stateMapping: {
    created: "created",
    paid: "paid",
    refund_pending: "refunding",
    refund_review: "refunding",
    closed: "closed",
  },
};
