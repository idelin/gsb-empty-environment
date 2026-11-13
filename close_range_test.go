package netsec

import (
	"errors"
	"reflect"
	"testing"
)

func TestSimulateWhitelistAndHoleRanges(t *testing.T) {
	table := DescriptorTable{
		Descriptors: []Descriptor{
			{FD: 9},
			{FD: 1},
			{FD: 4, Whitelist: true},
			{FD: 6},
			{FD: 2},
		},
	}
	calls := []CloseRangeCall{
		{First: 0, Last: 3},
		{First: 5, Last: 5},
		{First: 6, Last: 6},
		{First: 7, Last: 8},
		{First: 9, Last: 9},
	}

	report, err := Simulate(table, calls)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	if len(report.Leaked) != 0 {
		t.Fatalf("Leaked = %#v, want no leaks", report.Leaked)
	}
	wantBeforeExec := []Descriptor{{FD: 4, Whitelist: true}}
	if !reflect.DeepEqual(report.BeforeExec, wantBeforeExec) {
		t.Fatalf("BeforeExec = %#v, want %#v", report.BeforeExec, wantBeforeExec)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", report.Warnings)
	}
}

func TestSimulateCLOEXECOnlySetsBit(t *testing.T) {
	table := DescriptorTable{
		Shared: true,
		Descriptors: []Descriptor{
			{FD: 2},
			{FD: 4, Whitelist: true},
			{FD: 7},
		},
	}

	report, err := Simulate(table, []CloseRangeCall{{
		First: 0,
		Last:  CloseRangeEnd,
		Flags: CloseRangeCLOEXEC,
	}})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	if len(report.Leaked) != 0 {
		t.Fatalf("Leaked = %#v, want CLOEXEC descriptors to disappear at exec", report.Leaked)
	}
	wantBeforeExec := []Descriptor{
		{FD: 2, CloseOnExec: true},
		{FD: 4, CloseOnExec: true, Whitelist: true},
		{FD: 7, CloseOnExec: true},
	}
	if !reflect.DeepEqual(report.BeforeExec, wantBeforeExec) {
		t.Fatalf("BeforeExec = %#v, want %#v", report.BeforeExec, wantBeforeExec)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("CLOEXEC-only call produced warnings: %#v", report.Warnings)
	}
}

func TestSimulateInitialCLOEXECIsNotLeaked(t *testing.T) {
	table := DescriptorTable{
		Descriptors: []Descriptor{
			{FD: 1, CloseOnExec: true},
			{FD: 2},
		},
	}

	report, err := Simulate(table, []CloseRangeCall{{First: 2, Last: 2}})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if len(report.Leaked) != 0 {
		t.Fatalf("Leaked = %#v, want pre-existing O_CLOEXEC descriptor excluded", report.Leaked)
	}
}

func TestSimulatePartialCLOEXECRangeReportsLeak(t *testing.T) {
	table := DescriptorTable{
		Descriptors: []Descriptor{
			{FD: 2},
			{FD: 4, Whitelist: true},
			{FD: 7},
		},
	}

	report, err := Simulate(table, []CloseRangeCall{{
		First: 0,
		Last:  3,
		Flags: CloseRangeCLOEXEC,
	}})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	wantLeaked := []Descriptor{{FD: 7}}
	if !reflect.DeepEqual(report.Leaked, wantLeaked) {
		t.Fatalf("Leaked = %#v, want %#v", report.Leaked, wantLeaked)
	}
	wantBeforeExec := []Descriptor{
		{FD: 2, CloseOnExec: true},
		{FD: 4, Whitelist: true},
		{FD: 7},
	}
	if !reflect.DeepEqual(report.BeforeExec, wantBeforeExec) {
		t.Fatalf("BeforeExec = %#v, want %#v", report.BeforeExec, wantBeforeExec)
	}
}

func TestSimulateSharedTableWithoutUnshareWarns(t *testing.T) {
	table := DescriptorTable{
		Shared: true,
		Descriptors: []Descriptor{
			{FD: 1},
			{FD: 4, Whitelist: true},
			{FD: 6},
		},
	}
	calls := []CloseRangeCall{
		{First: 0, Last: 3},
		{First: 5, Last: CloseRangeEnd},
	}

	report, err := Simulate(table, calls)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	if len(report.Warnings) != 2 {
		t.Fatalf("Warnings = %#v, want two shared-table warnings", report.Warnings)
	}
	for i, warning := range report.Warnings {
		if warning.CallIndex != i || !reflect.DeepEqual(warning.Call, calls[i]) {
			t.Fatalf("Warnings[%d] = %#v, want call %#v", i, warning, calls[i])
		}
	}
}

func TestSimulateUnshareProtectsOtherThreads(t *testing.T) {
	table := DescriptorTable{
		Shared: true,
		Descriptors: []Descriptor{
			{FD: 1},
			{FD: 4, Whitelist: true},
			{FD: 6},
		},
	}
	calls := []CloseRangeCall{
		{First: 0, Last: 3, Flags: CloseRangeUnshare},
		{First: 5, Last: CloseRangeEnd},
	}

	report, err := Simulate(table, calls)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want unshare to make later calls private", report.Warnings)
	}
	if len(report.Leaked) != 0 {
		t.Fatalf("Leaked = %#v, want all non-whitelisted descriptors closed", report.Leaked)
	}
}

func TestSimulateLastUINTMAX(t *testing.T) {
	table := DescriptorTable{
		Descriptors: []Descriptor{
			{FD: 0, Whitelist: true},
			{FD: 2},
			{FD: 5},
		},
	}

	report, err := Simulate(table, []CloseRangeCall{{
		First: 2,
		Last:  CloseRangeEnd,
	}})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	wantBeforeExec := []Descriptor{{FD: 0, Whitelist: true}}
	if !reflect.DeepEqual(report.BeforeExec, wantBeforeExec) {
		t.Fatalf("BeforeExec = %#v, want %#v", report.BeforeExec, wantBeforeExec)
	}
	if len(report.Leaked) != 0 {
		t.Fatalf("Leaked = %#v, want no leaks", report.Leaked)
	}
}

func TestSimulateRejectsInvalidRange(t *testing.T) {
	_, err := Simulate(DescriptorTable{}, []CloseRangeCall{{
		First: 9,
		Last:  3,
	}})
	if !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("Simulate() error = %v, want ErrInvalidRange", err)
	}
}

func TestSimulateRejectsUnsupportedFlags(t *testing.T) {
	_, err := Simulate(DescriptorTable{}, []CloseRangeCall{{
		Last:  3,
		Flags: 1,
	}})
	if !errors.Is(err, ErrUnsupportedFlags) {
		t.Fatalf("Simulate() error = %v, want ErrUnsupportedFlags", err)
	}
}

func TestMinimalCleanupWhitelistBarrierAndHoles(t *testing.T) {
	table := DescriptorTable{
		Shared: true,
		Descriptors: []Descriptor{
			{FD: 10},
			{FD: 1},
			{FD: 6},
			{FD: 4, Whitelist: true},
			{FD: 2},
		},
	}

	want := []CloseRangeCall{
		{First: 1, Last: 2, Flags: CloseRangeUnshare},
		{First: 6, Last: 10},
	}
	got := MinimalCleanup(table)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MinimalCleanup() = %#v, want %#v", got, want)
	}

	report, err := Simulate(table, got)
	if err != nil {
		t.Fatalf("Simulate() generated plan error = %v", err)
	}
	if len(report.Leaked) != 0 {
		t.Fatalf("generated plan leaked %#v", report.Leaked)
	}
	if len(report.Warnings) != 0 {
		t.Fatalf("generated plan warned for shared table: %#v", report.Warnings)
	}
}

func TestMinimalCleanupSpansHolesWithoutWhitelist(t *testing.T) {
	table := DescriptorTable{
		Descriptors: []Descriptor{
			{FD: 1},
			{FD: 9},
		},
	}

	want := []CloseRangeCall{{First: 1, Last: 9}}
	got := MinimalCleanup(table)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MinimalCleanup() = %#v, want %#v", got, want)
	}
}

func TestMinimalCleanupAllWhitelisted(t *testing.T) {
	table := DescriptorTable{
		Shared: true,
		Descriptors: []Descriptor{
			{FD: 1, Whitelist: true},
			{FD: 9, Whitelist: true},
		},
	}

	if got := MinimalCleanup(table); len(got) != 0 {
		t.Fatalf("MinimalCleanup() = %#v, want no calls", got)
	}
}
