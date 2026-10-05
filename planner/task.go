package planner

import (
	"io/ioutil"
	"os"
	"path/filepath"
)

// Task is a build step that produces generated files.
type Task struct {
	Name    string
	Version string // bump when the task definition changes
	Deps    []Dep  // static dependencies; more may be discovered at run time
	Outputs []string
	Run     func(ctx *RunContext) error
}

// RunContext is passed to Task.Run. It records which files the task actually
// read and which dependencies it discovered while running.
type RunContext struct {
	p          *Planner
	task       *Task
	reads      map[string]string // file -> signature at read time
	discovered []Dep
}

// ReadFile reads a file inside the build root and records an implicit content
// dependency on it. The planner verifies after the run that the file did not
// change while the task was executing; if it did, the result is discarded.
func (c *RunContext) ReadFile(path string) ([]byte, error) {
	data, err := ioutil.ReadFile(c.p.abs(path))
	if err != nil {
		return nil, err
	}
	c.reads[path] = hashBytes(data)
	c.DependContent(path)
	return data, nil
}

// WriteFile writes a file inside the build root, creating parent directories.
func (c *RunContext) WriteFile(path string, data []byte) error {
	abs := c.p.abs(path)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	return ioutil.WriteFile(abs, data, 0644)
}

// DependContent declares a content dependency discovered while running.
func (c *RunContext) DependContent(node string) {
	c.discovered = append(c.discovered, ContentDep(node))
}

// DependOrder declares an order-only dependency discovered while running.
func (c *RunContext) DependOrder(node string) {
	c.discovered = append(c.discovered, OrderDep(node))
}
