package pathsplit_test

import (
	"fmt"

	"prescreen.local/ps-847c68412f/pathsplit"
)

func ExampleSplit() {
	fs := pathsplit.MapFS{
		"/":                   {Kind: pathsplit.KindDir},
		"/app":                {Kind: pathsplit.KindDir},
		"/app/index.php":      {Kind: pathsplit.KindFile},
		"/uploads":            {Kind: pathsplit.KindDir},
		"/uploads/avatar.jpg": {Kind: pathsplit.KindFile},
	}
	cfg := pathsplit.Config{
		FS:         fs,
		ScriptDirs: []string{"/app"},
		UploadDirs: []string{"/uploads"},
	}

	// A legitimate script with PATH_INFO.
	res, err := pathsplit.Split(cfg, "/app/index.php/extra/path")
	fmt.Printf("script: %+v, err: %v\n", res, err)

	// The avatar attack is refused because it sits under an upload directory.
	res, err = pathsplit.Split(cfg, "/uploads/avatar.jpg/x.php")
	fmt.Printf("avatar split result: %v, err: %v\n", res, err)

	// Output:
	// script: &{ScriptFilename:/app/index.php ScriptName:/app/index.php PathInfo:/extra/path ResolvedPath:/app/index.php}, err: <nil>
	// avatar split result: <nil>, err: pathsplit: execution inside an upload directory is forbidden
}
