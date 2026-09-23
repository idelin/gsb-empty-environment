// Command demo walks through every integrity guarantee of the proof package.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"chunkproof/proof"
)

func section(title string) {
	fmt.Printf("\n=== %s ===\n", title)
}

func main() {
	dir, err := os.MkdirTemp("", "chunkproof-demo-")
	must(err)
	defer os.RemoveAll(dir)

	// ---- 1. Build a package: 3 objects, reference graph 0 <- 1 <- {0,1} ----
	section("1. 构建包（对象顺序/数量/引用图由根摘要承诺）")
	objs := []proof.Object{
		{Kind: "blob", Name: "alpha", Payload: bytes.Repeat([]byte("A"), 100)},
		{Kind: "blob", Name: "beta", Refs: []uint32{0}, Payload: bytes.Repeat([]byte("B"), 200)},
		{Kind: "tree", Name: "gamma", Refs: []uint32{0, 1}, Payload: []byte("ROOT-REFS")},
	}
	pkg, err := proof.Build("demo-pkg", 1, 64, objs) // 64 字节小块，制造多个分块
	must(err)
	fmt.Printf("包 id=%s version=%d 对象数=%d 分块数=%d 负载=%d 字节\n",
		pkg.Manifest.PkgID, pkg.Manifest.Version, len(pkg.Manifest.Objects),
		len(pkg.Manifest.Chunks), len(pkg.Payload))
	fmt.Printf("可信根摘要（带外分发）: %x\n", pkg.Root[:12])

	path := filepath.Join(dir, "demo.env")
	must(proof.WriteEnvelopeFile(path, pkg))
	fmt.Printf("已写单文件包: %s\n", path)

	// ---- 2. Open + independent verification with the trusted root ----------
	section("2. 独立验证器（不信任文件内自报根，只信带外根摘要）")
	env, err := proof.OpenEnvelope(path)
	must(err)
	defer env.Close()
	ver, err := proof.NewVerifierFromEnvelope(env, pkg.Root)
	must(err)
	fmt.Printf("初始状态: %s（finish 之前只允许局部声明）\n", ver.Status())

	// ---- 3. Read arbitrary range subset: verify as you read ---------------
	section("3. 按范围读取任意子集，边读边校验，仅声明局部已验")
	cache := proof.NewChunkCache()
	data, ev, err := proof.ReadRange(ver, env, cache, 50, 130)
	must(err)
	fmt.Printf("范围 [50,180) 读取 %d 字节，证据分块=%v，状态仍为=%s\n",
		len(data), chunkIndexList(ev), ver.Status())

	obj1, _, err := proof.ReadObject(ver, env, cache, 1)
	must(err)
	fmt.Printf("对象 1 (%q/%q) refs=%v 已通过对象级验证；整包状态=%s\n",
		obj1.Name, obj1.Kind, obj1.Refs, ver.Status())

	// ---- 4. Premature finish fails ----------------------------------------
	section("4. 未读全时 finish 必须失败（删除末块同理）")
	if _, err := ver.Finish(); err != nil {
		fmt.Printf("finish 被拒（符合预期）: %v\n", err)
	}

	// ---- 5. Feed remaining chunks via version-bound cache, then finish -----
	section("5. 补齐全部分块（缓存命中复用，复用前仍做摘要复核）后 finish")
	for i := 0; i < ver.ChunkCount(); i++ {
		_, cev, err := proof.ObtainChunk(ver, env, cache, uint32(i))
		must(err)
		if i < 3 {
			fmt.Printf("分块 %d: fromCache=%v\n", i, cev.FromCache)
		}
	}
	rep, err := ver.Finish()
	must(err)
	fmt.Printf("finish 成功: status=%s chunks=%d/%d objects=%d/%d\n",
		rep.Status, rep.ChunksSeen, rep.ChunksTotal, rep.ObjectSeen, rep.ObjectsTotal)
	must(ver.MustBeComplete())

	// ---- 6. Version isolation: old root cannot verify new package ----------
	section("6. 版本隔离：旧证明不能拼到新包")
	pkgV2, err := proof.Build("demo-pkg", 2, 64, objs)
	must(err)
	fmt.Printf("v1 根=%x\nv2 根=%x\n", pkg.Root[:8], pkgV2.Root[:8])
	if _, err := proof.NewVerifier(pkgV2.Manifest, pkg.Root); errors.Is(err, proof.ErrRootMismatch) {
		fmt.Printf("用 v1 根验证 v2 被拒（符合预期）: %v\n", err)
	} else {
		fail("旧根居然通过了新版本")
	}
	// Old cached v1 chunk cannot serve v2 (different hash key -> miss).
	if _, ok := cache.Get("demo-pkg", 2, 0, pkgV2.Manifest.Chunks[0].Hash); !ok {
		fmt.Println("v1 缓存块对 v2 查询未命中：不会无摘要复核复用旧块")
	}

	// ---- 7. Attack table ----------------------------------------------------
	section("7. 攻击表（每种篡改都必须被独立验证器拒绝）")
	attackDeleteLastChunk(pkg)
	attackSwapChunks(pkg)
	attackTamperPayload(path, pkg.Root, dir)
	attackTamperManifest(pkg)
	attackPoisonedCache(pkg)

	fmt.Println("\n全部场景演示完成。")
}

func chunkIndexList(ev proof.RangeEvidence) []uint32 {
	out := make([]uint32, len(ev.Chunks))
	for i, c := range ev.Chunks {
		out[i] = c.Index
	}
	return out
}

func attackDeleteLastChunk(pkg *proof.Package) {
	type hide struct{ m map[uint32]bool }
	_ = hide{}
	// In-memory source that hides the last chunk.
	src := &hidingFetcher{pkg: pkg, hidden: map[uint32]bool{
		uint32(len(pkg.Manifest.Chunks) - 1): true,
	}}
	v, _ := proof.NewVerifier(pkg.Manifest, pkg.Root)
	for i := 0; i < v.ChunkCount()-1; i++ {
		b, err := src.FetchChunk(uint32(i))
		must(err)
		must(v.AcceptChunk(uint32(i), b))
	}
	_, err := v.Finish()
	fmt.Printf("- 删除末块（前块全部正确）: %v\n", classify(err))
}

func attackSwapChunks(pkg *proof.Package) {
	v, _ := proof.NewVerifier(pkg.Manifest, pkg.Root)
	src := &hidingFetcher{pkg: pkg}
	b2, _ := src.FetchChunk(2)
	err := v.AcceptChunk(1, b2)
	fmt.Printf("- 块换序（块2的字节按索引1提交）: %v\n", classify(err))
}

func attackTamperPayload(path string, trusted proof.RootSummary, dir string) {
	raw, err := os.ReadFile(path)
	must(err)
	raw[10] ^= 0xFF
	tp := filepath.Join(dir, "tampered.env")
	must(os.WriteFile(tp, raw, 0o644))
	env, err := proof.OpenEnvelope(tp)
	must(err)
	defer env.Close()
	v, _ := proof.NewVerifier(env.Manifest(), trusted)
	b0, err := env.FetchChunk(0)
	must(err)
	err = v.AcceptChunk(0, b0)
	fmt.Printf("- 磁盘负载篡改: %v\n", classify(err))
}

func attackTamperManifest(pkg *proof.Package) {
	// Re-encode a manifest claiming a different package id.
	raw := pkg.Manifest.EncodeCanonical()
	m, err := proof.DecodeManifest(proof.ScopeManifest, raw)
	must(err)
	m.PkgID = "attacker-pkg"
	_, err = proof.NewVerifier(m, pkg.Root)
	fmt.Printf("- 清单篡改（换包 id）后仍用旧根: %v\n", classify(err))

	// Local chunk evidence must not mask a missing necessary object: a manifest
	// that lists fewer objects than the payload covers fails geometry checks.
	m2, _ := proof.DecodeManifest(proof.ScopeManifest, raw)
	m2.Objects = m2.Objects[:2]
	err = m2.Validate()
	fmt.Printf("- 清单缺失必要对象（局部块证据无法掩盖）: %v\n", classify(err))
}

func attackPoisonedCache(pkg *proof.Package) {
	c := proof.NewChunkCache()
	src := &hidingFetcher{pkg: pkg}
	b0, _ := src.FetchChunk(0)
	must(c.Put(pkg.Manifest.PkgID, pkg.Manifest.Version, 0, pkg.Manifest.Chunks[0].Hash, b0))
	// Directly corrupt the cached backing bytes.
	if err := c.CorruptForTest(pkg.Manifest.PkgID, pkg.Manifest.Version, 0, pkg.Manifest.Chunks[0].Hash); err != nil {
		fail(err.Error())
	}
	v, _ := proof.NewVerifier(pkg.Manifest, pkg.Root)
	_, ev, err := proof.ObtainChunk(v, src, c, 0)
	switch {
	case err != nil:
		fmt.Printf("- 缓存被投毒且无回源: %v\n", classify(err))
	case ev.FromCache:
		fail("投毒缓存被当作命中")
	default:
		fmt.Println("- 缓存被投毒: 命中前摘要复核失败，按未命中回源并重新验证")
	}
}

type hidingFetcher struct {
	pkg    *proof.Package
	hidden map[uint32]bool
}

func (f *hidingFetcher) FetchChunk(index uint32) ([]byte, error) {
	if f.hidden[index] {
		return nil, fmt.Errorf("chunk %d unavailable", index)
	}
	var off uint64
	for _, c := range f.pkg.Manifest.Chunks[:index] {
		off += uint64(c.Bytes)
	}
	n := f.pkg.Manifest.Chunks[index].Bytes
	out := make([]byte, n)
	copy(out, f.pkg.Payload[off:off+uint64(n)])
	return out, nil
}

func classify(err error) string {
	switch {
	case err == nil:
		return "意外通过！！"
	case errors.Is(err, proof.ErrNotAllChunks):
		return "拒绝: " + err.Error()
	case errors.Is(err, proof.ErrChunkHash):
		return "拒绝: " + err.Error()
	case errors.Is(err, proof.ErrRootMismatch):
		return "拒绝: " + err.Error()
	case errors.Is(err, proof.ErrBadManifest):
		return "拒绝: " + err.Error()
	default:
		return "拒绝: " + err.Error()
	}
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "FATAL:", msg)
	os.Exit(1)
}
