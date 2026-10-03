/** 复杂嵌套业务订单样例（测试与示例共用）。 */
export function buildOrder(): Record<string, unknown> {
  return {
    orderId: 'SO-20261003-0001',
    channel: 'app',
    placedAt: '2026-10-03T10:24:31+08:00',
    user: {
      id: 88421,
      name: '张三',
      vipLevel: 3,
      address: {
        province: '上海',
        city: '上海市',
        district: '浦东新区',
        detail: '世纪大道 100 号',
        geo: { lng: 121.5, lat: 31.23 },
      },
      contacts: [
        { type: 'mobile', value: '138****0000' },
        { type: 'email', value: 'zhangsan@example.com' },
      ],
    },
    items: [
      {
        sku: 'SKU-1001',
        title: '机械键盘',
        price: 399.0,
        qty: 1,
        attrs: { color: '黑', switch: '茶轴' },
      },
      {
        sku: 'SKU-2002',
        title: '显示器 27"',
        price: 1599.5,
        qty: 2,
        attrs: { color: '银', size: '27寸' },
      },
    ],
    payment: {
      method: 'alipay',
      total: 3598.0,
      currency: 'CNY',
      installments: 3,
      vouchers: [{ code: 'V-88', off: 100 }],
    },
    logistics: {
      carrier: '顺丰',
      waybillNo: 'SF1234567890',
      routes: [
        { node: '上海青浦仓', at: '2026-10-03T12:00:00+08:00' },
        { node: '上海浦东分拨', at: '2026-10-03T18:30:00+08:00' },
      ],
    },
    remark: '含特殊字符的备注: "}{][\\ 与 \n 换行、unicode 世',
    tags: ['加急', '礼品包装'],
    ext: null,
  };
}

export function buildOrderJson(): string {
  return JSON.stringify(buildOrder(), null, 2);
}
