package main

import "fmt"

// CostTable is a non-negative integer cost table. Per-label entries override
// the defaults. RenamePair keys have the form "old>new".
type CostTable struct {
	Insert      int            `json:"insert"`
	Delete      int            `json:"delete"`
	Rename      int            `json:"rename"`
	InsertLabel map[string]int `json:"insertLabel,omitempty"`
	DeleteLabel map[string]int `json:"deleteLabel,omitempty"`
	RenamePair  map[string]int `json:"renamePair,omitempty"`
}

// DefaultCosts returns unit costs for all operations.
func DefaultCosts() *CostTable {
	return &CostTable{Insert: 1, Delete: 1, Rename: 1}
}

// Ins is the raw cost of inserting a node with the given label.
func (c *CostTable) Ins(label string) int {
	if v, ok := c.InsertLabel[label]; ok {
		return v
	}
	return c.Insert
}

// Del is the raw cost of deleting a single node with the given label
// (its children are lifted into its place; there is no extra charge
// for the subtree).
func (c *CostTable) Del(label string) int {
	if v, ok := c.DeleteLabel[label]; ok {
		return v
	}
	return c.Delete
}

// Ren is the raw cost of renaming one label to another.
func (c *CostTable) Ren(old, new string) int {
	if old == new {
		return 0
	}
	if v, ok := c.RenamePair[old+">"+new]; ok {
		return v
	}
	return c.Rename
}

// Validate checks that every cost is a non-negative integer.
func (c *CostTable) Validate() error {
	if c.Insert < 0 || c.Delete < 0 || c.Rename < 0 {
		return fmt.Errorf("default costs must be non-negative")
	}
	for l, v := range c.InsertLabel {
		if v < 0 {
			return fmt.Errorf("negative insert cost for label %q", l)
		}
	}
	for l, v := range c.DeleteLabel {
		if v < 0 {
			return fmt.Errorf("negative delete cost for label %q", l)
		}
	}
	for k, v := range c.RenamePair {
		if v < 0 {
			return fmt.Errorf("negative rename cost for pair %q", k)
		}
	}
	return nil
}
