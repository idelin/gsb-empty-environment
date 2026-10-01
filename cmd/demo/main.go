// Demo of the causal-tree CRDT acceptance scenario:
// two clients concurrently type '!' and '?' after "hello".
package main

import (
	"fmt"

	"causaltree/crdt"
)

func main() {
	// Alice types "hello"; each character's parent is the previous one.
	alice := crdt.New("alice")
	parent := crdt.RootID()
	for _, r := range "hello" {
		n, _ := alice.Insert(parent, r)
		parent = n.ID
	}
	oID := parent // ID of the final 'o'

	// Bob syncs the initial document.
	bob := crdt.New("bob")
	bob.Merge(alice)
	fmt.Printf("before concurrency: alice=%q bob=%q\n", alice.String(), bob.String())

	// Concurrent edits: both insert right after 'o', offline.
	bang, _ := alice.Insert(oID, '!')
	quest, _ := bob.Insert(oID, '?')
	fmt.Printf("concurrent inserts: '!' id=%v parent=%v | '?' id=%v parent=%v\n",
		bang.ID, bang.Parent, quest.ID, quest.Parent)
	fmt.Printf("same parent: %v\n", bang.Parent == quest.Parent)

	// Sync both ways.
	alice.Merge(bob)
	bob.Merge(alice)

	fmt.Printf("after sync: alice=%q bob=%q\n", alice.String(), bob.String())
	fmt.Printf("converged:  %v\n", alice.String() == bob.String())
}
