# deltaupdate

文件差量更新工具：客户端持有旧文件，服务器提供新文件的块摘要（manifest），
客户端生成"下载 + 复用"计划并安全落地。

## 设计

- **块摘要**：新文件按固定块长切分，每块记录 rsync 风格滚动弱校验 + SHA-256
  强校验，外加整文件 SHA-256。
- **计划（plan）**：用滚动弱校验扫描旧文件，候选位置再用强校验确认——复用
  完全按内容判定，与偏移无关。因此块可以搬移、可以在新文件中重复出现（多处
  复用旧文件中同一处内容）。不足块长的末块与旧文件尾部比对。
- **应用（apply）**：写入 `目标.part` 临时文件，每完成一块更新 `目标.journal`
  进度记录。重启恢复时**不信任**日志本身，所有块一律按内容重新校验：
  日志存在时只校验其声明完成的块；日志丢失（如确认文件已写盘但进度未落盘）
  则全量扫描临时文件内容恢复进度。复用块在拷贝时再次校验内容，校验失败自动
  回退为下载。
- **提交门**：只有临时文件整文件 SHA-256 与 manifest 一致，才 fsync 并原子
  rename 替换旧版本；否则旧版本原样保留。

## 用法

```sh
# 服务器侧：生成 manifest 与块文件
deltaupdate split -block 65536 -out /srv/blocks newfile

# 客户端侧：生成计划并应用（可中断，重跑即续传）
deltaupdate plan /srv/blocks/manifest.json oldfile > plan.json
deltaupdate apply plan.json oldfile newfile -blocks /srv/blocks
```

## 测试

```sh
go test ./...
```

覆盖：同内容多位置复用、末块变短、坏复用块回退下载、崩溃续传、
确认文件写盘后进度记录丢失的重启、整文件摘要门保护旧版本。
