// Package netsec provides an importable trajectory-anonymization component
// for Go data-publishing services.
//
// The pipeline removes direct identifiers, samples observations, generalizes
// space and time, perturbs generalized coordinates, suppresses rare locations
// and rare temporal routines, checks k-anonymity for combinations of quasi
// identifiers, and assesses re-identification risk before publication.
//
// Typical use:
//
//	anonymizer, err := netsec.NewAnonymizer(netsec.Config{K: 5})
//	if err != nil {
//		log.Fatal(err)
//	}
//	result, err := anonymizer.Publish(dataset)
//	if err != nil {
//		// A *netsec.PublicationError contains the full risk report.
//		log.Fatal(err)
//	}
//	// result.Records contains only sanitized, generalized trajectories.
package netsec
