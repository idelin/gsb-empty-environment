// Command archivecert builds and checks fixed archive-continuity
// certificates.
package main

import (
	"flag"
	"fmt"
	"os"

	"formal.local/w3-a2a139a7a9"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "build":
		err = build(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "verify-all":
		err = verifyAll(os.Args[2:])
	case "audit":
		err = audit(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "archivecert:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  archivecert build       [-out cert.json] archive.json
  archivecert verify      -clocking clock.json cert.json
  archivecert verify-all [-opts opts.json] cert.json
  archivecert audit       -clocking clock.json archive.json`)
	os.Exit(2)
}

func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	out := fs.String("out", "-", "output certificate JSON path")
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	a, err := archiveproof.LoadArchive(fs.Arg(0))
	if err != nil {
		return err
	}
	c, err := archiveproof.BuildCertificate(a, nil)
	if err != nil {
		return err
	}
	return writeJSON(*out, c)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	clockPath := fs.String("clocking", "", "clocking JSON path")
	fs.Parse(args)
	if fs.NArg() != 1 || *clockPath == "" {
		usage()
	}
	c, err := archiveproof.LoadCertificate(fs.Arg(0))
	if err != nil {
		return err
	}
	clk, err := archiveproof.LoadClocking(*clockPath)
	if err != nil {
		return err
	}
	p, err := c.Verify(clk)
	if err != nil {
		return err
	}
	return archiveproof.EncodeJSON(os.Stdout, p)
}

func verifyAll(args []string) error {
	fs := flag.NewFlagSet("verify-all", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		usage()
	}
	c, err := archiveproof.LoadCertificate(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.VerifyAll(nil); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, `{"ok":true}`)
	return nil
}

func audit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	clockPath := fs.String("clocking", "", "clocking JSON path")
	fs.Parse(args)
	if fs.NArg() != 1 || *clockPath == "" {
		usage()
	}
	a, err := archiveproof.LoadArchive(fs.Arg(0))
	if err != nil {
		return err
	}
	clk, err := archiveproof.LoadClocking(*clockPath)
	if err != nil {
		return err
	}
	ok, err := a.SecureAt(clk)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "{\"ok\":%v}\n", ok)
	return nil
}

func writeJSON(path string, v any) error {
	if path == "-" {
		return archiveproof.EncodeJSON(os.Stdout, v)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return archiveproof.EncodeJSON(f, v)
}
