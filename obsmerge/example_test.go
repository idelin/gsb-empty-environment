package obsmerge_test

import (
	"fmt"

	"example.org/domainreissue/q073/obsmerge"
)

func ExampleEngine() {
	// The CRM is most trusted for names; the billing system for emails.
	priority := obsmerge.FieldPriority(0, nil, map[string]map[string]int{
		"name":  {"crm": 2},
		"email": {"billing": 2},
	})
	eng := obsmerge.NewEngine[string](priority)

	_ = eng.Apply(obsmerge.Observation[string]{
		ID:            "fetch-1",
		SourceID:      "crm",
		SourceVersion: "2026-11-12",
		PageType:      "profile",
		BusinessKey:   "cust-7",
		CoveredFields: []string{"name"},
		Fields: map[string]obsmerge.Nullable[string]{
			"name": obsmerge.Of("Acme"),
		},
	})
	_ = eng.Apply(obsmerge.Observation[string]{
		ID:            "fetch-2",
		SourceID:      "billing",
		SourceVersion: "2026-11-12",
		PageType:      "account",
		BusinessKey:   "cust-7",
		// This page covers both fields; email is explicitly empty.
		CoveredFields: []string{"name", "email"},
		Fields: map[string]obsmerge.Nullable[string]{
			"name":  obsmerge.Of("Acme Billing"),
			"email": obsmerge.Empty[string](),
		},
	})

	name, _ := eng.Field("cust-7", "name")
	email, _ := eng.Field("cust-7", "email")
	phone, _ := eng.Field("cust-7", "phone")

	fmt.Printf("name=%s (%s, source=%s)\n", name.Value, name.Status, name.Sources[0].SourceID)
	fmt.Printf("email=%s\n", email.Status)
	fmt.Printf("phone=%s\n", phone.Status)

	// Output:
	// name=Acme (resolved, source=crm)
	// email=deleted
	// phone=unknown
}

func ExampleEngine_retractAndVerify() {
	eng := obsmerge.NewEngine[string](obsmerge.StaticPriority(0, map[string]int{
		"official": 2,
	}))

	_ = eng.Apply(obsmerge.Observation[string]{
		ID: "bad", SourceID: "official", SourceVersion: "v1", PageType: "p",
		BusinessKey: "k1", CoveredFields: []string{"name"},
		Fields: map[string]obsmerge.Nullable[string]{"name": obsmerge.Of("Wrong")},
	})
	_ = eng.Apply(obsmerge.Observation[string]{
		ID: "good", SourceID: "partner", SourceVersion: "v1", PageType: "p",
		BusinessKey: "k1", CoveredFields: []string{"name"},
		Fields: map[string]obsmerge.Nullable[string]{"name": obsmerge.Of("Right")},
	})

	_ = eng.Retract("bad")
	report, _ := eng.ReplayAndVerify()

	field, _ := eng.Field("k1", "name")
	fmt.Printf("value=%s match=%t source=%s@%s\n",
		field.Value, report.Match,
		field.Sources[0].SourceID, field.Sources[0].SourceVersion)

	// Output:
	// value=Right match=true source=partner@v1
}
