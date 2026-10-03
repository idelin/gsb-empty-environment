import type { JsonValue } from "../src/index.ts";

/** Builds a complex nested business order document. */
export function buildOrder(id: number, city: string): JsonValue {
  return {
    orderId: `ORD-${id}`,
    channel: id % 2 === 0 ? "web" : "app",
    placedAt: "2026-09-30T16:00:00.000Z",
    user: {
      id: 1000 + id,
      name: `user-${id}`,
      vip: id % 3 === 0,
      address: {
        city,
        district: "Binjiang",
        geo: { lat: 30.2084, lng: 120.212 },
        tags: ["home", "office"],
      },
    },
    items: [
      {
        sku: "SKU-PHONE-01",
        qty: 1,
        price: 4999.5,
        attrs: { color: "black", warranty: { years: 2, onsite: true } },
      },
      {
        sku: "SKU-CASE-09",
        qty: 2,
        price: 39.9,
        attrs: { color: "clear", warranty: null },
      },
      {
        sku: "SKU-CHARGER-33",
        qty: 1,
        price: 129,
        attrs: { color: "white", warranty: { years: 1, onsite: false } },
      },
    ],
    payment: {
      method: "alipay",
      installments: 3,
      coupon: { code: "VIP-100", discount: 100 },
    },
    logistics: {
      carrier: "SF-Express",
      routes: [
        { hub: "Hangzhou", etaHours: 2 },
        { hub: "Shanghai", etaHours: 20 },
      ],
      notes: "leave at door \"B-201\", call \\ before arrival\n谢谢",
    },
  };
}

export function buildOrderRaw(id: number, city: string): string {
  return JSON.stringify(buildOrder(id, city));
}
