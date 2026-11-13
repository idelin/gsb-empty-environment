# 外部权重路径逃出模型目录

模型图节点可引用外部权重文件，攻击者用点段、符号链接或同名替换读取宿主凭据。给Go模型宿主实现可导入的外部权重冻结包：图已解析为节点与initializer列表，每个外部initializer含唯一tensorID、相对location、offset、length、sha256和期望dtype/shape；location是UTF-8路径段，拒绝空段、点段、绝对路径、NUL、反斜杠和百分号转义。以模型目录句柄逐段不跟随symlink打开，末项须普通文件；记录设备/inode/文件代次和只读句柄。offset+length用数学整数检查且在文件大小内，长度恰等于dtype×shape元素数，按句柄区间计算摘要；同文件区间重叠仅在tensorID、摘要、dtype、shape全部相同且声明shared=true时允许。冻结全部initializer后生成绑定图摘要、文件身份和区间的权重清单，推理只从冻结句柄读取；任一文件变化、短读或清单不符使整个模型实例失效。用注入文件系统在逐段打开和推理之间替换目录/文件、硬链接宿主凭据、截断、重叠区间及模型目录句柄保持有效但其路径删除重建，核对宿主根外哨兵字节从未进入张量回调。

## Package API

The importable implementation is package `netsec` in this module.

```go
model, err := netsec.Freeze(netsec.Spec{
    Root:        modelDir, // opened as an os.Root directory handle
    GraphDigest: netsec.SumDigest(graphBytes),
    Initializers: []netsec.Initializer{{
        TensorID: "layers.0.weight",
        Location: "layer0/weight.bin",
        Offset:   0,
        Length:   24,
        SHA256:   netsec.SumDigest(weightBytes),
        Dtype:    netsec.Float32,
        Shape:    []int64{6},
    }},
})
if err != nil {
    return err // freezing failed; no tensor data is exposed
}
defer model.Close()

weights, err := model.Tensor("layers.0.weight") // frozen-handle read
manifest := model.Manifest()                  // graph digest + file IDs + ranges
err = model.VerifyManifest(manifest)
```

`Spec.FileSystem` is an injection seam. Production code leaves it nil to use
`OSFileSystem`; tests can supply a `FileSystem`/`DirHandle`/`FileHandle` that
models swaps, hardlinks, truncation, and root-path replacement.

Key guarantees:

- Locations are validated as relative UTF-8 segments and are opened one
  component at a time from the model directory handle. Entry identity is
  checked across each open, so a Lstat-to-open replacement is rejected.
- Final entries must be regular files with link count 1. Device, inode,
  generation, size, and timestamps are recorded in the manifest.
- `offset+length` and `dtype*shape` use overflow-checked big-integer math.
- Overlapping intervals in one file are rejected unless the tensor ID, digest,
  dtype, and shape are identical and both declarations are `Shared`.
- All inference reads use the frozen read-only handles, never the location
  path. Replacing or deleting a path (including the model directory path) does
  not redirect reads.
- A truncation, append, metadata change, short read, digest mismatch, or
  manifest mismatch invalidates the whole `Model`; subsequent tensor reads
  return `ErrModelInvalid`.
