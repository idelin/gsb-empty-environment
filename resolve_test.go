package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

func mustVer(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestScenario(t *testing.T) {
	data, err := os.ReadFile("scenario.json")
	if err != nil {
		t.Fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(&in)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Resolve() {
		t.Fatal("期望解析成功")
	}
	expect := map[string]choice{
		"acme-auth":   {ver: mustVer(t, "9.9.9"), source: SourcePublic},
		"acme-log":    {ver: mustVer(t, "2.0.0"), source: SourcePublic},
		"acme-secret": {ver: mustVer(t, "1.0.0"), source: SourcePrivate},
		"crypto":      {ver: mustVer(t, "2.0.0"), source: SourcePublic},
		"util":        {ver: mustVer(t, "2.0.0"), source: SourcePublic},
	}
	if !equalAssign(expect, r.assigned) {
		t.Fatalf("解析结果不符: 得到 %v", r.assigned)
	}
	for _, pkg := range []string{"acme-auth", "acme-log"} {
		if c := r.assigned[pkg]; !(r.isPrivate[pkg] && c.source == SourcePublic) {
			t.Fatalf("期望 %s 被依赖混淆到公源", pkg)
		}
	}
	if r.order[0] != "acme-auth" {
		t.Fatalf("第一次混淆替代应为 acme-auth，实际决策序 %v", r.order)
	}
	if len(r.Rejections["util"]) == 0 || len(r.Rejections["crypto"]) == 0 {
		t.Fatalf("期望 util 与 crypto 存在高候选回退记录: %v", r.Rejections)
	}
	if err := Verify(&in); err != nil {
		t.Fatal("穷举核验失败:", err)
	}
}

func TestRandomSmallGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	versions := []string{"0.1.0", "0.2.0", "1.0.0"}
	ranges := []string{"*", ">=0.2.0", "<1.0.0", ">=0.1.0 <1.0.0", "=0.2.0", "!=0.2.0", ">=0.2.0 <1.0.0"}
	for iter := 0; iter < 300; iter++ {
		n := 3 + rng.Intn(3)
		names := make([]string, n)
		in := Input{
			Roots: map[string]string{},
			Sources: map[string]Source{
				SourcePublic:  {Packages: map[string]map[string]VersionInfo{}},
				SourcePrivate: {Packages: map[string]map[string]VersionInfo{}},
			},
		}
		for i := 0; i < n; i++ {
			names[i] = fmt.Sprintf("p%d", i)
			in.Roots[names[i]] = ">=0.0.0"
			if rng.Intn(2) == 0 {
				in.PrivatePackages = append(in.PrivatePackages, names[i])
			}
		}
		for _, src := range []string{SourcePrivate, SourcePublic} {
			for _, name := range names {
				for _, vs := range versions {
					if rng.Intn(2) == 0 {
						continue
					}
					vi := VersionInfo{Deps: map[string]string{}}
					nd := rng.Intn(3)
					for k := 0; k < nd; k++ {
						dep := names[rng.Intn(n)]
						if dep == name {
							continue
						}
						vi.Deps[dep] = ranges[rng.Intn(len(ranges))]
					}
					if in.Sources[src].Packages[name] == nil {
						in.Sources[src].Packages[name] = map[string]VersionInfo{}
					}
					in.Sources[src].Packages[name][vs] = vi
				}
			}
		}
		if err := Verify(&in); err != nil {
			data, _ := json.MarshalIndent(&in, "", "  ")
			t.Fatalf("iter %d 核验失败: %v\n输入: %s", iter, err, data)
		}
	}
}
