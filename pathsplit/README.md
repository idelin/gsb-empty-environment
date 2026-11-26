# pathsplit

`pathsplit` is an importable Go package that safely splits an HTTP request URL
path into a PHP `SCRIPT_FILENAME` / `SCRIPT_NAME` and `PATH_INFO` for a FastCGI
gateway. It evaluates everything against an in-memory virtual file listing, so
it never touches the real disk and is deterministic and easy to test.

Import path:

```go
import "prescreen.local/ps-847c68412f/pathsplit"
```

## Inputs

- `Config.FS`: virtual site-root listing implementing `pathsplit.FS`
  (`MapFS` is provided), including regular files, directories and symbolic
  links with their targets.
- `Config.ScriptDirs`: root-relative directories allowed to contain executable
  PHP.
- `Config.UploadDirs`: root-relative directories that must never be executed.
- The raw URL path passed to `Split`.

## When a segment becomes a script

A segment is used as `SCRIPT_FILENAME` only when all of the following hold:

1. Its normalized name ends in `.php`, case-insensitively. Trailing dots and
   spaces (and their percent-encoded forms) are stripped before the suffix is
   checked, e.g. `index.php%20`, `index.php.` and `index.PHP` all match.
2. The corresponding entry really exists as a file in the virtual listing.
3. Its virtual path is inside a configured script directory.
4. Every intermediate and trailing symlink is recursively resolved; symlink
   loops are rejected and the resolved path stays inside the site root.
5. Neither the virtual path nor the resolved path is inside an upload
   directory.

When several `.php` segments qualify, the foremost one wins and the remainder
becomes `PATH_INFO`.

Upload directories always take precedence over the script whitelist, so a path
under an upload directory is never split for execution — even when a file is
literally named `shell.php` or the request looks like
`/uploads/avatar.jpg/x.php`.

## Rejected paths

`Split` returns a non-nil error for explicit security violations:

- `ErrMalformedPath`: invalid percent-encoding, or a path containing a NUL
  byte (`%00`), a `..` segment or a backslash anywhere (leading path or
  `PATH_INFO`).
- `ErrUploadDenied`: a candidate lives in, or resolves into, an upload
  directory.
- `ErrSymlinkLoop`: symlink resolution visits the same link more than once.
- `ErrOutsideRoot`: a symlink target resolves above the site root.

When the path simply does not map to any runnable PHP script, `Split` returns
`(nil, nil)` so the gateway can fall back to static serving or its own 404.

## Usage

```go
fs := pathsplit.MapFS{
    "/app/index.php":            {Kind: pathsplit.KindFile},
    "/app/link.php":             {Kind: pathsplit.KindSymlink, Target: "/realdir/index.php"},
    "/realdir/index.php":        {Kind: pathsplit.KindFile},
    "/uploads/avatar.jpg":       {Kind: pathsplit.KindFile},
}

cfg := pathsplit.Config{
    FS:         fs,
    ScriptDirs: []string{"/app", "/realdir"},
    UploadDirs: []string{"/uploads"},
}

res, err := pathsplit.Split(cfg, r.URL.EscapedPath())
switch {
case errors.Is(err, pathsplit.ErrUploadDenied),
     errors.Is(err, pathsplit.ErrSymlinkLoop),
     errors.Is(err, pathsplit.ErrOutsideRoot),
     errors.Is(err, pathsplit.ErrMalformedPath):
    // Reject the request (typically HTTP 403/400).
case err != nil:
    // Configuration error.
case res == nil:
    // No runnable PHP: serve statically.
default:
    // Build the concrete FastCGI parameters.
    scriptFilename := filepath.Join(documentRoot, res.ResolvedPath)
}
```

`res.ScriptFilename` is the root-relative virtual path used for
`SCRIPT_FILENAME`/`SCRIPT_NAME`; `res.ResolvedPath` is the same path after full
symlink resolution; `res.PathInfo` is the decoded trailing portion.
