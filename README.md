# stroke_rasterizer

等宽折线笔触栅格化库（C++17）。

## 特性

- 等宽折线描边，端点为平头（butt）端帽
- 三种转角样式：斜接（miter）、圆角（round）、截角（bevel）
- 斜接长度（顶点到尖角距离 / 半宽）超过 miterLimit 时自动退化为截角
- 像素按中心点 (x+0.5, y+0.5) 是否落在笔触内判定
- 圆弧与斜接长度均使用平方距离比较，避免开方
- 零长线段（连续重复点）自动忽略；180 度折返安全处理
- 位图置位幂等，连接处重叠区域不会重复计数

## 构建与测试

    cmake -B build
    cmake --build build
    ctest --test-dir build --output-on-failure

## 用法

    #include "stroke_rasterizer.h"

    stroke::Options opt;
    opt.width = 3.0;
    opt.join = stroke::JoinStyle::kMiter;
    opt.miterLimit = 4.0;

    stroke::Bitmap bm = stroke::rasterizePolyline({{0, 0}, {20, 0}, {20, 20}}, opt);
    bool covered = bm.at(10, 0);

rasterizePixels 返回扫描线顺序的唯一像素坐标列表，与位图计数一致。

## 实现说明

- 每条线段栅格化为矩形（中心点满足 0 <= t <= len 且 |s| <= r）
- 每个内部顶点按样式补形：圆角填圆盘，截角填三角形，斜接填四边形（两个三角形）
- 斜接点由法向约束 n1.(m-p) = r、n2.(m-p) = r 解二元线性方程组得到
- 共线（含折返）时两侧矩形已重叠，无需补形；圆角样式为折返端补半圆
- 退化到单个点时绘制半径为半宽的圆点
