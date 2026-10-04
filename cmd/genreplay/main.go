// genreplay 生成 testdata/replay.json：用引擎计算期望结果并落盘，
// 供 TestReplayJSON 离线重放校验。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"hexgrid"
)

type replayQuery struct {
	Start  hexgrid.Hex  `json:"start"`
	K      int          `json:"k"`
	Target *hexgrid.Hex `json:"target,omitempty"`
	Expect expect       `json:"expect"`
}

type expect struct {
	Reachable []hexgrid.Hex `json:"reachable"`
	Steps     int           `json:"steps"`
	Path      []hexgrid.Hex `json:"path,omitempty"`
	PageLoads int           `json:"pageLoads"`
}

type replayFile struct {
	Comment   string        `json:"comment"`
	ChunkSize int           `json:"chunkSize"`
	Passable  []hexgrid.Hex `json:"passable"`
	Obstacles []hexgrid.Hex `json:"obstacles"`
	Queries   []replayQuery `json:"queries"`
}

func main() {
	chunk := 2
	obstacles := []hexgrid.Hex{{Q: 1, R: 0}, {Q: 0, R: 1}, {Q: -1, R: 1}, {Q: 2, R: -2}}
	ob := map[hexgrid.Hex]bool{}
	for _, o := range obstacles {
		ob[o] = true
	}
	var passable []hexgrid.Hex
	for q := -3; q <= 3; q++ {
		for r := -3; r <= 3; r++ {
			if !ob[hexgrid.Hex{Q: q, R: r}] {
				passable = append(passable, hexgrid.Hex{Q: q, R: r})
			}
		}
	}

	type qspec struct {
		start  hexgrid.Hex
		k      int
		target *hexgrid.Hex
	}
	t := func(q, r int) *hexgrid.Hex { h := hexgrid.Hex{Q: q, R: r}; return &h }
	specs := []qspec{
		{hexgrid.Hex{Q: 0, R: 0}, 0, nil},         // K=0：只返回起点
		{hexgrid.Hex{Q: 0, R: 0}, 3, t(2, -1)},    // 跨分区 + 字典序决胜路径
		{hexgrid.Hex{Q: -3, R: 3}, 4, t(1, -1)},   // 负坐标起点，目标在 K 步内不可达（steps=-1）
		{hexgrid.Hex{Q: 3, R: -3}, 5, nil},        // 角落起点、较大 K，覆盖正坐标边缘分区
		{hexgrid.Hex{Q: -2, R: -2}, 2, t(-3, -1)}, // 负坐标跨区短查询
	}

	store := hexgrid.NewStore(chunk, passable)
	eng := hexgrid.NewEngine(store)

	rf := replayFile{
		Comment:   "战棋六边形网格回放样例：轴坐标、6邻居偏移固定；分区仅为存储边界。由 cmd/genreplay 生成。",
		ChunkSize: chunk,
		Passable:  passable,
		Obstacles: obstacles,
	}
	for _, s := range specs {
		res, err := eng.Run(hexgrid.Query{Start: s.start, K: s.k, Target: s.target})
		if err != nil {
			fmt.Fprintf(os.Stderr, "query %+v: %v\n", s, err)
			os.Exit(1)
		}
		rf.Queries = append(rf.Queries, replayQuery{
			Start:  s.start,
			K:      s.k,
			Target: s.target,
			Expect: expect{
				Reachable: res.Reachable,
				Steps:     res.Steps,
				Path:      res.Path,
				PageLoads: res.Stats.PageLoads,
			},
		})
	}

	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("testdata/replay.json", append(data, '\n'), 0o644); err != nil {
		panic(err)
	}
	fmt.Println("wrote testdata/replay.json with", len(rf.Queries), "queries")
}
