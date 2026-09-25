# silseg — 多轨静音检测与片段切分

纯 Python 3.11 标准库实现。样本数组与采样率由本地夹具（`fixtures.py`）提供，
不接触任何音频设备。测试使用 `unittest`。

## 运行

```bash
python3.11 demo.py                          # 可运行样例（端到端演示 + 自检）
python3.11 -m unittest discover -s tests -v # 测试套件
```

## 结构

- `silseg/track.py` — `Track`：按绝对索引寻址的单轨样本序列，允许缺口（gap）。
- `silseg/detector.py` — 窗口 RMS + 迟滞状态机 + 分轨合并策略（`any`/`all`），
  支持流式处理、回退重算与 checkpoint。
- `silseg/segments.py` — 由逐窗有声序列提取片段：最短有声/最短静音过滤、
  前后保留量扩展、交叠合并。
- `silseg/incremental.py` — `IncrementalSegmenter`：后补音频 / 修订静音标记时
  局部重分段，输出片段合并/拆分补丁事件；稳定片段 ID；checkpoint 恢复。
- `fixtures.py` — 确定性夹具（8000 Hz，窗口 80 样本 = 10 ms）。
- `demo.py` — 可运行样例。

## 声明（语义约定）

- **窗口量化**：检测以固定窗口为粒度，窗口对齐绝对样本索引，因此结果与
  音频分块方式无关；片段边界量化到窗口边界（保留量按样本精确加减）。
- **时长取整**：`min_silence` / `min_sound`（样本数）按窗口向上取整后比较。
- **迟滞**：窗口 RMS 明显高于 `high_threshold` → 有声；明显低于
  `low_threshold` → 静音；介于两阈值之间 → 保持前一状态。
- **数值容差**：阈值比较使用 `rel_tol=1e-9, abs_tol=1e-12`
  （`silseg/constants.py`）。RMS 落在阈值容差内不触发状态翻转（保守规则）。
- **缺口 vs 真实静音**：窗口内某轨无数据（缺口）→ 该轨不投票；所有轨都无
  数据 → 窗口判为静音。导出时缺口位置为 `None`，真实静音为 `0.0`，二者
  在导出层严格区分。
- **合并策略**：`any` = 任一有声轨即有声；`all` = 所有有数据的轨均有声才有声。
- **处理顺序**：先按 `min_sound` 吸收短噪点，再按 `min_silence` 桥接短静音，
  然后加前后保留量，最后合并交叠/相接区间并裁剪到 `[0, total)`。
- **延迟**：`latency_samples = window + ceil(min_silence/window)*window +
  post_pad`——一个片段的终点需观察到足够长的静音后才能确认。
- **片段 ID 与补丁**：ID 形如 `S0001`，稳定可追溯。新旧片段按样本重叠匹配，
  重叠最大者保留原 ID（平手取先）；被合并的旧 ID 以
  `{"op":"remove","reason":"merged","merged_into":...}` 注销；拆分时一块保留
  原 ID，其余获得新 ID；已导出片段的边界变化以
  `{"op":"revise","old":[s,e],"new":[s,e]}` 事件更正。
- **静音标记**：`revise_silence(start, end, is_silence)` 覆盖与其相交窗口的
  检测结果，覆盖后的序列同样参与最短持续与保留量规则。
- **末尾不足一窗**：`finalize` 时以已有样本计算 RMS；后续音频落入该窗口会
  触发回退重算。
- **checkpoint**：`checkpoint()` 输出 JSON 可序列化状态（含逐窗历史，可在
  候选静音期间安全打点），`restore()` 后继续处理与全量参考结果一致。

## 补丁事件示例

```python
{"op": "add",    "id": "S0003", "start": 6240, "end": 7120}
{"op": "revise", "id": "S0001", "old": [720, 1680], "new": [720, 4880]}
{"op": "remove", "id": "S0002", "reason": "merged", "merged_into": "S0001"}
{"op": "remove", "id": "S0004", "reason": "retracted"}
```
