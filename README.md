# silence_split —— 多轨静音检测与片段切分

纯 Python 3.11 标准库实现(仅 `math` / `dataclasses` / `json` 等),测试基于 `unittest`。
样本数组与采样率由本地夹具(`silence_split/fixtures.py`)提供,不接触任何音频设备。

## 检测管线

1. **窗口 RMS**:按 `window_size` 个采样点一窗计算均方根(`math.fsum` 求和,数值稳定)。
2. **迟滞(双门限施密特触发)**:RMS 高于 `open_threshold` 进入有声,低于
   `close_threshold` 进入静音,两者之间保持原状态,避免门限附近抖动。
3. **最短持续规则**:
   - 静音持续 ≥ `min_silence` 才构成切分点,更短的静音间隔被合并进同一片段;
   - 有声持续 < `min_sound` 的短噪点被吸收为静音(不重置静音计时);
     调小 `min_sound` 则同一噪点会断开静音 —— 两种规则分别对应"合并/断开"。
4. **前后保留量**:片段导出区间为核心有声区向前后各扩 `pre_pad` / `post_pad`;
   相邻片段保留区交叠时以核心间中点裁剪,**导出区间两两不相交,不会重复导出样本**。
5. **多轨策略**:`strategy="any"` 任一轨有声即有声;`strategy="all"` 全部轨有声才有声。

## 延迟与数值容差声明

* 检测粒度为一个窗口;`min_silence` / `min_sound` 以采样点给出,按窗口向上取整。
* **静音确认延迟上界** = `min_silence + window_size` 采样点
  (`DetectorConfig.confirmation_latency_samples`)。
* **片段导出延迟上界** = 确认延迟 + `post_pad`
  (`DetectorConfig.export_latency_samples`)。
* **数值容差** = `NUMERIC_TOLERANCE = 1e-9`(相对):RMS 落在门限容差死区内时
  保持原迟滞状态,边界行为确定、可复现。

## 缺口与真实静音

`AudioStore` 是稀疏区间存储:未写入的区域是**缺口(未知)**,检测器在缺口处停滞
(`Session.stalled`),不做任何确认;写入零值的区域是**真实静音**,参与静音确认。
`retract(start, end)` 把区间置零(已知静音),`erase(start, end)` 抹除回缺口。

## 修订与补丁事件

后补音频或修订静音标记(`add_marker` / `clear_markers`)会触发**局部重分段**:
分类从受影响窗口之前最近的"迟滞死区外同步点"重算,未受影响窗口的 RMS 结果走缓存;
随后对新旧片段列表做 diff,只输出变化部分的补丁事件:

| 事件 | 含义 |
| --- | --- |
| `SegmentCreated` | 新确认的片段 |
| `SegmentRevised` | 边界变化;已导出片段携带递增的 `version` |
| `SegmentMerged` | 多段合并,最前段的稳定 ID 存活 |
| `SegmentSplit` | 一段拆分,首段保留原 ID |
| `SegmentDiscarded` | 片段失效(如补音被抹除回缺口) |

**稳定片段 ID** 由核心起点派生(`seg-%08x`):重分段后起点不变则 ID 不变;
合并保留最前段 ID,拆分后首段保留原 ID,因而跨修订可追溯。
已导出片段由 `Exporter` 登记版本,修订事件驱动版本递增、合并登记与拆分复活,
全程留痕于 `Exporter.history`。

## checkpoint

`StreamingDetector.checkpoint()` 导出可 JSON 序列化的完整状态(迟滞状态、
标记历史、已提交区间、候选静音进度),即使在**候选静音期间**保存,
`restore` 后继续推进的结果也与不间断全量参考逐点一致。

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 27 个测试
python3.11 examples/demo.py                   # 可运行样例
```

## 目录结构

```
silence_split/
  constants.py   数值容差声明
  core.py        配置、窗口 RMS、迟滞、最短持续规则、核心区间
  segments.py    片段模型、稳定 ID、保留量扩展与交叠裁剪
  streaming.py   增量检测器、静音确认提交、checkpoint
  audio.py       稀疏音频存储(缺口 vs 真实静音)
  events.py      补丁事件类型
  session.py     会话:局部重分段、事件 diff、导出登记处
  fixtures.py    本地合成信号夹具
tests/           unittest 测试(对照全量参考)
examples/demo.py 可运行样例
```
