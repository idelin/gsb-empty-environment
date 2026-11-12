# 随机化加载布局精确抽样

沙箱加载器希望在所有合法布局中均匀随机选择，以免偏置降低地址随机化强度。实现供Go加载宿主调用的布局包，最多六个带对齐要求的区域放入至多32页地址域，保留区不可占，区域不可重叠；精确计数合法布局并按注入随机字节无偏抽样，不能以随机重试代替完整分布。用小域枚举核对每个布局排名、零解与取消后的随机流位置。

## 设计

把布局问题建模为页地址域上的有序决策树：从左到右扫描 `[0, DomainPages)`，
在每个未保留页上按固定顺序选择“放置某个尚未放置且合法的区域”或“留空”。
每条根到叶路径恰好对应一个合法布局，因此：

- `Count` 用记忆化动态规划精确统计叶子数，状态为 `(当前页, 已放置区域位掩码)`，
  规模至多 `(32+1) × 2^6`，不枚举布局本身；
- `Rank` / `Unrank` 在同一棵树上累加各分支权重，实现布局与 `[0, Count)` 排名之间的双射；
- `Sample` 先对排名区间做拒绝式均匀取数（拒绝只发生在字节到整数的折叠上，
  与布局合法性无关），再用 `Unrank` 精确逆排名，保证每个合法布局概率严格相等，
  不使用“随机试放、失败重试”。

`Stream` 包装注入的随机字节源并通过 `Position` 暴露已消耗字节数。零解、上下文
取消（取数前检查）或随机流提前结束均不消耗随机字节；取数中途取消时位置精确
停在已读字节处，宿主可据此接续或复现随机流。

## 用法

```go
spec := netsec.Spec{
    DomainPages: 32,
    Regions: []netsec.Region{
        {SizePages: 4, AlignPages: 8},
        {SizePages: 2, AlignPages: 2},
    },
    Reserved: []netsec.Reservation{{StartPage: 10, Pages: 2}},
}

total, err := netsec.Count(spec)          // 精确合法布局数
stream := netsec.NewStream(randReader)    // 注入随机字节源
layout, err := netsec.Sample(ctx, spec, stream)
_ = stream.Position()                     // 已消耗的随机字节数

rank, _ := netsec.Rank(spec, layout)      // 布局 -> 排名
again, _ := netsec.Unrank(spec, rank)     // 排名 -> 布局
```

## 验证

- 小域穷举器独立枚举全部布局，与 `Count` 逐一核对，并验证 `Rank`/`Unrank`
  在完整分布上构成无重复、无遗漏的双射；
- 卡方检验核对 `DrawUniform`（非 2 的幂模数）与端到端 `Sample` 的均匀性；
- 覆盖零解（保留区占满、对齐无解）、越界排名、非法规格、随机流 EOF 位置、
  取消前后的流位置，以及 32 页 6 区域最大规模下的全排名往返。
