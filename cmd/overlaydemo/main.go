package main

import (
	"fmt"

	"layermerge"
)

func main() {
	layers := []*layermerge.Layer{
		layermerge.MustLayer("base",
			layermerge.File("app.conf", "v1"),
			layermerge.Dir("data",
				layermerge.File("old.db", "x"),
				layermerge.File("cache", "c"),
			),
			layermerge.File("README", "base"),
		),
		layermerge.MustLayer("patch",
			layermerge.Whiteout("README"),
			layermerge.Dir("data",
				layermerge.Opaque(),
				layermerge.File("new.db", "y"),
			),
		),
		layermerge.MustLayer("top",
			layermerge.File("app.conf", "v2"),
			layermerge.Hardlink("app.conf.link", "/app.conf"),
			layermerge.Dir("data", layermerge.File("extra", "e")),
		),
	}

	snaps, err := layermerge.Expand(layers...)
	if err != nil {
		panic(err)
	}
	for _, s := range snaps {
		fmt.Printf("=== after layer %d (%s) ===\n%s\n", s.Layer, layers[s.Layer].Name, s.Root.Dump())
	}
}
