package gittree_test

import (
	"context"
	"errors"
	"fmt"

	"example.org/offlinecli/gittree"
)

func Example() {
	const algo = gittree.SHA1
	source := gittree.NewMemorySource()

	// Build: root/ { a/ { hello.txt } }
	blobID := source.Add(algo, gittree.TypeBlob, []byte("hello\n"))
	dirID, err := source.AddTree(algo, []gittree.Entry{
		{Mode: gittree.ModeRegular, Name: "hello.txt", ID: blobID},
	})
	if err != nil {
		panic(err)
	}
	rootID, err := source.AddTree(algo, []gittree.Entry{
		{Mode: gittree.ModeTree, Name: "a", ID: dirID},
	})
	if err != nil {
		panic(err)
	}

	// RootID is the trusted anchor, e.g. taken from a signed manifest.
	proof := &gittree.MembershipProof{
		RootID: rootID,
		Path: []gittree.PathStep{
			{Name: "a", TreeID: rootID},
			{Name: "hello.txt", TreeID: dirID},
		},
	}

	verifier := gittree.NewVerifier(source)
	result, err := verifier.VerifyFile(context.Background(), proof)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s %s %q\n", result.Mode, result.ID, result.Content)

	// A symlink entry can be inspected with VerifyEntry but never accepted
	// as a regular file by VerifyFile.
	linkID := source.Add(algo, gittree.TypeBlob, []byte("hello.txt"))
	rootWithLink, err := source.AddTree(algo, []gittree.Entry{
		{Mode: gittree.ModeSymlink, Name: "hello-link", ID: linkID},
	})
	if err != nil {
		panic(err)
	}
	linkProof := &gittree.MembershipProof{
		RootID: rootWithLink,
		Path:   []gittree.PathStep{{Name: "hello-link", TreeID: rootWithLink}},
	}
	_, err = verifier.VerifyFile(context.Background(), linkProof)
	fmt.Println(errors.Is(err, gittree.ErrSymlinkNotFile))

	// Output:
	// 100644 ce013625030ba8dba906f756967f9e9ca394464a "hello\n"
	// true
}
