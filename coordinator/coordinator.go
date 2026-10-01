// Package coordinator implements a CDC timeline coordinator that
// interleaves DDL and DML events. When a DDL event arrives, the
// coordinator inserts a global barrier into the data flow: it drains
// all in-flight DML parsed under the old schema, applies the new
// schema to the metadata cache, and only then resumes the flow of
// new DML.
package coordinator

import (
	"context"
	"sync"
)

// Coordinator reads a single ordered event stream and emits parsed
// rows, barriers, and errors in a causally consistent order.
type Coordinator struct {
	reg     *Registry
	workers int
}

// NewCoordinator builds a coordinator over reg. workers controls how
// many goroutines parse DML concurrently; the barrier semantics are
// independent of the worker count.
func NewCoordinator(reg *Registry, workers int) *Coordinator {
	if workers < 1 {
		workers = 1
	}
	return &Coordinator{reg: reg, workers: workers}
}

// parseJob binds a raw DML record to the schema snapshot that was
// current when the record passed the coordinator. Binding the snapshot
// at dispatch time is what "cuts" the metadata cache: a later DDL can
// never retroactively change how this record is parsed.
type parseJob struct {
	event  DMLEvent
	schema Schema
}

// Run consumes events from in until it is closed or ctx is cancelled,
// and returns the output stream. The output order guarantees:
//
//	DML_Old*  ->  Barrier(from,to)  ->  DML_New*
//
// i.e. a barrier is never emitted until every DML dispatched before
// the DDL has been fully parsed and emitted downstream.
func (c *Coordinator) Run(ctx context.Context, in <-chan Event) <-chan Output {
	out := make(chan Output)
	go func() {
		defer close(out)

		jobs := make(chan parseJob)
		var inflight sync.WaitGroup // DML dispatched but not yet emitted
		var workersWG sync.WaitGroup
		for i := 0; i < c.workers; i++ {
			workersWG.Add(1)
			go func() {
				defer workersWG.Done()
				for j := range jobs {
					out <- parse(j)
					inflight.Done()
				}
			}()
		}

	loop:
		for {
			select {
			case <-ctx.Done():
				break loop
			case ev, ok := <-in:
				if !ok {
					break loop
				}
				switch e := ev.(type) {
				case DMLEvent:
					schema, ok := c.reg.Get(e.Table)
					if !ok {
						out <- ErrorOutput{Table: e.Table, Err: &UnknownTableError{Table: e.Table}}
						continue
					}
					inflight.Add(1)
					jobs <- parseJob{event: e, schema: schema}
				case DDLEvent:
					// Global barrier: drain every DML dispatched under
					// the old schema before touching the metadata cache.
					inflight.Wait()

					from, ok := c.reg.Get(e.Table)
					if !ok {
						out <- ErrorOutput{Table: e.Table, Err: &UnknownTableError{Table: e.Table}}
						continue
					}
					to, err := c.reg.Apply(e)
					if err != nil {
						out <- ErrorOutput{Table: e.Table, Err: err}
						continue
					}
					out <- BarrierOutput{
						Table:       e.Table,
						FromVersion: from.Version,
						ToVersion:   to.Version,
					}
				}
			}
		}

		// Shutdown: drain remaining DML, then stop workers.
		inflight.Wait()
		close(jobs)
		workersWG.Wait()
	}()
	return out
}

// parse aligns raw DML fields to the bound schema snapshot. A field
// count mismatch is reported, never silently misaligned.
func parse(j parseJob) Output {
	if len(j.event.Raw) != len(j.schema.Columns) {
		return ErrorOutput{
			Table: j.event.Table,
			Err: &FieldCountError{
				Table:         j.event.Table,
				SchemaVersion: j.schema.Version,
				Got:           len(j.event.Raw),
				Want:          len(j.schema.Columns),
			},
		}
	}
	fields := make(map[string]string, len(j.schema.Columns))
	for i, col := range j.schema.Columns {
		fields[col.Name] = j.event.Raw[i]
	}
	return RowOutput{Row: ParsedRow{
		Table:         j.event.Table,
		SchemaVersion: j.schema.Version,
		Fields:        fields,
	}}
}
