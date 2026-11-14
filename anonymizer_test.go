package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func textElement(tag Tag, valueVR vr, value string) *element {
	return &element{tag: tag, vr: valueVR, value: []byte(value)}
}

func sequenceElement(tag Tag, items ...*dataset) *element {
	return &element{tag: tag, vr: vrSQ, items: items}
}

func testMeta(transferSyntax string, sopInstanceUID string) *dataset {
	return &dataset{elements: []*element{
		textElement(Tag{0x0002, 0x0002}, vrUI, "1.2.840.10008.5.1.4.1.1.2"),
		textElement(Tag{0x0002, 0x0003}, vrUI, sopInstanceUID),
		textElement(Tag{0x0002, 0x0010}, vrUI, transferSyntax),
		textElement(Tag{0x0002, 0x0012}, vrUI, "1.2.826.0.1.3680043.10.999"),
		textElement(Tag{0x0002, 0x0013}, vrSH, "TEST"),
		textElement(Tag{0x0002, 0x0016}, vrAE, "EXPORT-SOURCE"),
	}}
}

func buildFile(t *testing.T, ds *dataset, enc fileEncoding, sopInstanceUID string) []byte {
	t.Helper()
	ts := "1.2.840.10008.1.2.1"
	switch {
	case enc.deflated:
		ts = "1.2.840.10008.1.2.1.99"
	case !enc.explicit && enc.little:
		ts = "1.2.840.10008.1.2"
	case enc.explicit && !enc.little:
		ts = "1.2.840.10008.1.2.2"
	}
	data, err := encodeDICOM(&dicomFile{
		part10: true,
		meta:   testMeta(ts, sopInstanceUID),
		data:   ds,
		enc:    enc,
	})
	if err != nil {
		t.Fatalf("encode test file: %v", err)
	}
	return data
}

func parseForTest(t *testing.T, data []byte) *dicomFile {
	t.Helper()
	f, err := parseDICOM(data)
	if err != nil {
		t.Fatalf("parse DICOM: %v", err)
	}
	return f
}

func valueOf(t *testing.T, ds *dataset, tag Tag) string {
	t.Helper()
	e := ds.find(tag)
	if e == nil {
		return ""
	}
	return trimText(e.value)
}

func baseImageDataset() *dataset {
	root := &dataset{elements: []*element{
		textElement(Tag{0x0008, 0x0005}, vrCS, "ISO_IR 192"),
		textElement(Tag{0x0008, 0x0016}, vrUI, "1.2.840.10008.5.1.4.1.1.2"),
		textElement(Tag{0x0008, 0x0018}, vrUI, "1.2.3.4.5"),
		textElement(Tag{0x0008, 0x0020}, vrDA, "20240115"),
		textElement(Tag{0x0008, 0x0021}, vrDA, "20240115"),
		textElement(Tag{0x0008, 0x0030}, vrTM, "121500"),
		textElement(Tag{0x0008, 0x0050}, vrSH, "ACC-998877"),
		textElement(Tag{0x0008, 0x0060}, vrCS, "CT"),
		textElement(Tag{0x0008, 0x0080}, vrLO, "City Hospital"),
		textElement(Tag{0x0008, 0x0090}, vrPN, "Referring^Doctor"),
		textElement(Tag{0x0008, 0x1030}, vrLO, "CHEST CT"),
		textElement(Tag{0x0008, 0x103E}, vrLO, "AXIAL"),
		textElement(Tag{0x0008, 0x1090}, vrLO, "Acme Scanner"),
		textElement(Tag{0x0010, 0x0010}, vrPN, "Zhang^San"),
		textElement(Tag{0x0010, 0x0020}, vrLO, "MRN-12345"),
		textElement(Tag{0x0010, 0x0030}, vrDA, "19800229"),
		textElement(Tag{0x0010, 0x0040}, vrCS, "M"),
		textElement(Tag{0x0010, 0x1010}, vrAS, "044Y"),
		textElement(Tag{0x0010, 0x4000}, vrLT, "Patient Zhang^San, MRN-12345"),
		textElement(Tag{0x0018, 0x0015}, vrCS, "CHEST"),
		textElement(Tag{0x0020, 0x000D}, vrUI, "1.2.3.4.6"),
		textElement(Tag{0x0020, 0x000E}, vrUI, "1.2.3.4.7"),
		textElement(Tag{0x0020, 0x0010}, vrSH, "STUDY-42"),
		textElement(Tag{0x0020, 0x0052}, vrUI, "1.2.3.4.8"),
		textElement(Tag{0x0020, 0x4000}, vrLT, "Image comment for Zhang San, MRN-12345"),
		textElement(Tag{0x0028, 0x0002}, vrUS, "\x01\x00"),
		textElement(Tag{0x0028, 0x0004}, vrCS, "MONOCHROME2"),
		textElement(Tag{0x0028, 0x0010}, vrUS, "\x02\x00"),
		textElement(Tag{0x0028, 0x0011}, vrUS, "\x02\x00"),

		textElement(Tag{0x0009, 0x0010}, vrLO, "VENDOR 1.0"),
		textElement(Tag{0x0009, 0x1001}, vrLO, "Zhang^San"),
		textElement(Tag{0x0009, 0x1002}, vrLO, "ACC-998877"),

		sequenceElement(Tag{0x0009, 0x1010}, &dataset{elements: []*element{
			textElement(Tag{0x0009, 0x1001}, vrLO, "NESTED-PRIVATE-NAME"),
			textElement(Tag{0x0010, 0x0010}, vrPN, "Nested^Name"),
		}}),

		sequenceElement(Tag{0x0040, 0x0275}, &dataset{elements: []*element{
			textElement(Tag{0x0008, 0x0050}, vrSH, "ACC-998877"),
			textElement(Tag{0x0040, 0x1001}, vrSH, "PROC-7788"),
			textElement(Tag{0x0008, 0x1155}, vrUI, "1.2.3.4.5"),
			textElement(Tag{0x0009, 0x1001}, vrLO, "Zhang^San"),
			sequenceElement(Tag{0x0008, 0x1140}, &dataset{elements: []*element{
				textElement(Tag{0x0008, 0x1155}, vrUI, "1.2.3.4.5"),
			}}),
		}}),

		sequenceElement(Tag{0x0070, 0x0001}, &dataset{elements: []*element{
			sequenceElement(Tag{0x0070, 0x0008}, &dataset{elements: []*element{
				textElement(Tag{0x0070, 0x0006}, vrUT, "Zhang San MRN-12345"),
			}}),
		}}),

		textElement(Tag{0x6000, 0x0010}, vrUS, "\x02\x00"),
		textElement(Tag{0x6000, 0x3000}, vrOW, "\x01\x02\x03\x04"),
		textElement(Tag{0x7FE0, 0x0010}, vrOW, "\xDE\xAD\xBE\xEF"),
	}}
	return root
}

func walkDatasets(ds *dataset, fn func(*dataset)) {
	if ds == nil {
		return
	}
	fn(ds)
	for _, e := range ds.elements {
		for _, item := range e.items {
			walkDatasets(item, fn)
		}
	}
}

func collectPrivateTags(ds *dataset) []Tag {
	var tags []Tag
	walkDatasets(ds, func(current *dataset) {
		for _, e := range current.elements {
			if e.tag.isPrivate() {
				tags = append(tags, e.tag)
			}
		}
	})
	return tags
}

func assertNotPresent(t *testing.T, data []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if bytes.Contains(data, []byte(value)) {
			t.Errorf("anonymized output still contains %q", value)
		}
	}
}

func TestAnonymizerRemovesIdentityEverywhere(t *testing.T) {
	input := buildFile(t, baseImageDataset(), fileEncoding{explicit: true, little: true}, "1.2.3.4.5")
	a := MustNewAnonymizer(
		WithSecret([]byte("test-secret-for-q0379")),
		WithUIDRoot("1.2.826.0.1.3680043.10.999"),
		WithDateShiftRangeDays(3650),
	)
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}

	out := parseForTest(t, output)
	if out.part10 != true {
		t.Fatal("output should remain a Part 10 file")
	}
	if private := collectPrivateTags(out.data); len(private) != 0 {
		t.Fatalf("private tags remain: %v", private)
	}
	if private := collectPrivateTags(out.meta); len(private) != 0 {
		t.Fatalf("private meta tags remain: %v", private)
	}

	for _, tag := range []Tag{
		{0x0008, 0x0050}, // AccessionNumber
		{0x0008, 0x0080}, // InstitutionName
		{0x0008, 0x0090}, // ReferringPhysicianName
		{0x0008, 0x1030}, // StudyDescription
		{0x0008, 0x103E}, // SeriesDescription
		{0x0010, 0x0010}, // PatientName
		{0x0010, 0x0020}, // PatientID
		{0x0010, 0x0040}, // PatientSex
		{0x0010, 0x1010}, // PatientAge
		{0x0010, 0x4000}, // PatientComments
		{0x0020, 0x0010}, // StudyID
		{0x0020, 0x4000}, // ImageComments
	} {
		if got := valueOf(t, out.data, tag); got != "" {
			t.Errorf("%s not cleared: %q", tag, got)
		}
	}

	if got := valueOf(t, out.data, Tag{0x0008, 0x0060}); got != "CT" {
		t.Errorf("Modality should be retained, got %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x0018, 0x0015}); got != "CHEST" {
		t.Errorf("BodyPartExamined should be retained, got %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x0008, 0x1090}); got != "Acme Scanner" {
		t.Errorf("Manufacturer should be retained, got %q", got)
	}
	pixel := out.data.find(Tag{0x7FE0, 0x0010})
	if pixel == nil || !bytes.Equal(pixel.value, []byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Errorf("pixel data was unexpectedly changed")
	}
	if out.data.find(Tag{0x6000, 0x3000}) != nil {
		t.Error("overlay annotation group was retained")
	}
	if out.data.find(Tag{0x0070, 0x0001}) != nil {
		t.Error("graphic annotation sequence was retained")
	}
	if got := valueOf(t, out.data, Tag{0x0012, 0x0062}); got != "YES" {
		t.Errorf("PatientIdentityRemoved = %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x0028, 0x0303}); got != "MODIFIED" {
		t.Errorf("LongitudinalTemporalInformationModified = %q", got)
	}

	studyDate := valueOf(t, out.data, Tag{0x0008, 0x0020})
	seriesDate := valueOf(t, out.data, Tag{0x0008, 0x0021})
	birthDate := valueOf(t, out.data, Tag{0x0010, 0x0030})
	if studyDate == "" || studyDate == "20240115" {
		t.Errorf("StudyDate was not replaced: %q", studyDate)
	}
	if seriesDate != studyDate {
		t.Errorf("equal original dates should shift consistently: %q != %q", studyDate, seriesDate)
	}
	if birthDate == "" || birthDate == "19800229" {
		t.Errorf("PatientBirthDate was not replaced: %q", birthDate)
	}

	sopUID := valueOf(t, out.data, Tag{0x0008, 0x0018})
	studyUID := valueOf(t, out.data, Tag{0x0020, 0x000D})
	seriesUID := valueOf(t, out.data, Tag{0x0020, 0x000E})
	if sopUID == "1.2.3.4.5" || studyUID == "1.2.3.4.6" || seriesUID == "1.2.3.4.7" {
		t.Error("instance UIDs were not replaced")
	}
	request := out.data.find(Tag{0x0040, 0x0275})
	if request == nil || len(request.items) != 1 {
		t.Fatal("RequestAttributesSequence missing after anonymization")
	}
	if got := valueOf(t, request.items[0], Tag{0x0008, 0x1155}); got != sopUID {
		t.Errorf("nested ReferencedSOPInstanceUID %q does not match replacement SOPInstanceUID %q", got, sopUID)
	}
	if got := valueOf(t, request.items[0], Tag{0x0040, 0x1001}); got != "" {
		t.Errorf("nested RequestedProcedureID was retained: %q", got)
	}
	if got := valueOf(t, out.meta, Tag{0x0002, 0x0003}); got != sopUID {
		t.Errorf("MediaStorageSOPInstanceUID %q does not match SOPInstanceUID %q", got, sopUID)
	}
	if got := valueOf(t, out.meta, Tag{0x0002, 0x0002}); got != "1.2.840.10008.5.1.4.1.1.2" {
		t.Errorf("SOP Class UID must be retained, got %q", got)
	}
	if got := valueOf(t, out.meta, Tag{0x0002, 0x0010}); got != "1.2.840.10008.1.2.1" {
		t.Errorf("Transfer Syntax UID must be retained, got %q", got)
	}

	assertNotPresent(t, output,
		"Zhang", "Nested^Name", "MRN-12345", "ACC-998877", "PROC-7788",
		"NESTED-PRIVATE-NAME", "VENDOR 1.0", "City Hospital",
		"1.2.3.4.5", "1.2.3.4.6", "1.2.3.4.7", "1.2.3.4.8",
	)
	for i := 0; i < 128; i++ {
		if output[i] != 0 {
			t.Fatalf("preamble byte %d was not zeroed", i)
		}
	}

	second := MustNewAnonymizer(WithSecret([]byte("second-secret")), WithDateShiftRangeDays(3650))
	reimported, err := second.Anonymize(output)
	if err != nil {
		t.Fatalf("re-anonymize output: %v", err)
	}
	reparsed := parseForTest(t, reimported)
	if len(collectPrivateTags(reparsed.data)) != 0 {
		t.Error("private tags reappeared after re-import")
	}
	assertNotPresent(t, reimported,
		"Zhang", "MRN-12345", "ACC-998877", "PROC-7788", "1.2.3.4.5",
	)
}

func TestPrivateTagWhitelistRetainsOnlyRequestedValues(t *testing.T) {
	ds := &dataset{elements: []*element{
		textElement(Tag{0x0010, 0x0010}, vrPN, "Private^Patient"),
		textElement(Tag{0x0010, 0x0020}, vrLO, "MRN-1"),
		textElement(Tag{0x0019, 0x0010}, vrLO, "ACME PRIVATE"),
		textElement(Tag{0x0019, 0x1001}, vrDS, "3.5"),
		textElement(Tag{0x0019, 0x1002}, vrLO, "Keep this protocol value"),
		textElement(Tag{0x0019, 0x1003}, vrLO, "MRN-1"),
		sequenceElement(Tag{0x0019, 0x1010}, &dataset{elements: []*element{
			textElement(Tag{0x0019, 0x1004}, vrLO, "nested private"),
			textElement(Tag{0x0010, 0x0010}, vrPN, "Nested^Patient"),
		}}),
	}}
	input := buildFile(t, ds, fileEncoding{explicit: true, little: true}, "1.2.3.4.10")
	a := MustNewAnonymizer(
		WithSecret([]byte("whitelist-secret")),
		WithPrivateTagWhitelist(NewTag(0x0019, 0x1001)),
	)
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	out := parseForTest(t, output)

	if got := valueOf(t, out.data, Tag{0x0019, 0x1001}); got != "3.5" {
		t.Errorf("whitelisted private value = %q, want 3.5", got)
	}
	if out.data.find(Tag{0x0019, 0x0010}) == nil {
		t.Error("private creator needed by whitelisted tag was removed")
	}
	if out.data.find(Tag{0x0019, 0x1002}) != nil {
		t.Error("non-whitelisted private tag was retained")
	}
	if out.data.find(Tag{0x0019, 0x1003}) != nil {
		t.Error("non-whitelisted identity-bearing private tag was retained")
	}
	if out.data.find(Tag{0x0019, 0x1010}) != nil {
		t.Error("non-whitelisted private sequence was retained")
	}
}

func TestWhitelistedPrivateSequenceIsSanitizedRecursively(t *testing.T) {
	ds := &dataset{elements: []*element{
		textElement(Tag{0x0019, 0x0010}, vrLO, "ACME SEQ"),
		sequenceElement(Tag{0x0019, 0x1010}, &dataset{elements: []*element{
			textElement(Tag{0x0019, 0x1002}, vrLO, "safe private value"),
			textElement(Tag{0x0019, 0x1003}, vrLO, "private identity"),
			textElement(Tag{0x0010, 0x0010}, vrPN, "Nested^Name"),
			textElement(Tag{0x0008, 0x1155}, vrUI, "1.2.3.4.11"),
		}}),
	}}
	input := buildFile(t, ds, fileEncoding{explicit: true, little: true}, "1.2.3.4.11")
	a := MustNewAnonymizer(
		WithSecret([]byte("whitelist-seq-secret")),
		WithPrivateTagWhitelist(NewTag(0x0019, 0x1010), NewTag(0x0019, 0x1002)),
	)
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	out := parseForTest(t, output)
	seq := out.data.find(Tag{0x0019, 0x1010})
	if seq == nil || len(seq.items) != 1 {
		t.Fatal("whitelisted private sequence missing")
	}
	item := seq.items[0]
	if got := valueOf(t, item, Tag{0x0019, 0x1002}); got != "safe private value" {
		t.Errorf("whitelisted nested value = %q", got)
	}
	if item.find(Tag{0x0019, 0x1003}) != nil {
		t.Error("nested non-whitelisted private tag was retained")
	}
	if got := valueOf(t, item, Tag{0x0010, 0x0010}); got != "" {
		t.Error("nested PatientName was retained")
	}
	if got := valueOf(t, item, Tag{0x0008, 0x1155}); got == "" || got == "1.2.3.4.11" {
		t.Errorf("nested UID was not remapped: %q", got)
	}
}

func TestUIDReferencesStayConsistentAcrossFiles(t *testing.T) {
	a := MustNewAnonymizer(WithSecret([]byte("cross-file-secret")))

	referenced := baseImageDataset()
	referenced.find(Tag{0x0008, 0x0018}).value = []byte("1.2.3.4.60")
	referenced.find(Tag{0x0020, 0x000D}).value = []byte("1.2.3.4.61")
	referenced.find(Tag{0x0020, 0x000E}).value = []byte("1.2.3.4.62")
	referenced.find(Tag{0x0040, 0x0275}).items[0].find(Tag{0x0008, 0x1155}).value = []byte("1.2.3.4.99")
	refInput := buildFile(t, referenced, fileEncoding{explicit: true, little: true}, "1.2.3.4.60")

	target := baseImageDataset()
	target.find(Tag{0x0008, 0x0018}).value = []byte("1.2.3.4.99")
	target.find(Tag{0x0020, 0x000D}).value = []byte("1.2.3.4.63")
	target.find(Tag{0x0020, 0x000E}).value = []byte("1.2.3.4.64")
	target.find(Tag{0x0040, 0x0275}).items[0].find(Tag{0x0008, 0x1155}).value = []byte("1.2.3.4.99")
	targetInput := buildFile(t, target, fileEncoding{explicit: true, little: true}, "1.2.3.4.99")

	refOut, err := a.Anonymize(refInput)
	if err != nil {
		t.Fatalf("anonymize referenced file: %v", err)
	}
	targetOut, err := a.Anonymize(targetInput)
	if err != nil {
		t.Fatalf("anonymize target file: %v", err)
	}
	refParsed := parseForTest(t, refOut)
	targetParsed := parseForTest(t, targetOut)
	refUID := valueOf(t, refParsed.data.find(Tag{0x0040, 0x0275}).items[0], Tag{0x0008, 0x1155})
	targetUID := valueOf(t, targetParsed.data, Tag{0x0008, 0x0018})
	if refUID != targetUID {
		t.Errorf("cross-file reference %q != target instance %q", refUID, targetUID)
	}
}

func TestDateShiftIsConsistentPerPatientButNotAcrossPatients(t *testing.T) {
	a := MustNewAnonymizer(WithSecret([]byte("date-secret")), WithDateShiftRangeDays(3650))

	makeInput := func(patientID, studyUID, sopUID string) []byte {
		ds := &dataset{elements: []*element{
			textElement(Tag{0x0008, 0x0016}, vrUI, "1.2.840.10008.5.1.4.1.1.2"),
			textElement(Tag{0x0008, 0x0018}, vrUI, sopUID),
			textElement(Tag{0x0008, 0x0020}, vrDA, "20240115"),
			textElement(Tag{0x0010, 0x0010}, vrPN, "Patient^"+patientID),
			textElement(Tag{0x0010, 0x0020}, vrLO, patientID),
			textElement(Tag{0x0010, 0x0030}, vrDA, "19800229"),
			textElement(Tag{0x0020, 0x000D}, vrUI, studyUID),
			textElement(Tag{0x0020, 0x000E}, vrUI, studyUID+".1"),
		}}
		return buildFile(t, ds, fileEncoding{explicit: true, little: true}, sopUID)
	}

	outputs := [][]byte{
		makeInput("P1", "1.2.3.10.1", "1.2.3.10.2"),
		makeInput("P1", "1.2.3.10.3", "1.2.3.10.4"),
		makeInput("P2", "1.2.3.11.1", "1.2.3.11.2"),
		makeInput("P3", "1.2.3.12.1", "1.2.3.12.2"),
	}
	dates := make([]string, len(outputs))
	birthDates := make([]string, len(outputs))
	for i, input := range outputs {
		result, err := a.Anonymize(input)
		if err != nil {
			t.Fatalf("anonymize %d: %v", i, err)
		}
		ds := parseForTest(t, result).data
		dates[i] = valueOf(t, ds, Tag{0x0008, 0x0020})
		birthDates[i] = valueOf(t, ds, Tag{0x0010, 0x0030})
		if dates[i] == "20240115" || birthDates[i] == "19800229" {
			t.Fatalf("date %d was not shifted", i)
		}
	}
	if dates[0] != dates[1] || birthDates[0] != birthDates[1] {
		t.Errorf("same patient received inconsistent date shifts: %q/%q vs %q/%q",
			dates[0], birthDates[0], dates[1], birthDates[1])
	}
	if dates[0] == dates[2] && dates[0] == dates[3] {
		t.Errorf("different patients all received the same study date shift: %q", dates[0])
	}
}

func TestBurnedInAnnotationIsRejectedByDefault(t *testing.T) {
	ds := baseImageDataset()
	ds.elements = append(ds.elements, textElement(Tag{0x0028, 0x0301}, vrCS, "YES"))
	input := buildFile(t, ds, fileEncoding{explicit: true, little: true}, "1.2.3.4.5")

	a := MustNewAnonymizer(WithSecret([]byte("burned-secret")))
	if _, err := a.Anonymize(input); !errors.Is(err, ErrBurnedInAnnotation) {
		t.Fatalf("expected ErrBurnedInAnnotation, got %v", err)
	}
	allowed := MustNewAnonymizer(
		WithSecret([]byte("burned-secret")),
		WithRejectBurnedInAnnotation(false),
	)
	if _, err := allowed.Anonymize(input); err != nil {
		t.Fatalf("allow burned-in annotation: %v", err)
	}
}

func TestImplicitVRUndefinedLengthSequence(t *testing.T) {
	var data []byte
	data = appendImplicitElement(data, Tag{0x0008, 0x0016}, "1.2.840.10008.5.1.4.1.1.2")
	data = appendImplicitElement(data, Tag{0x0008, 0x0018}, "1.2.3.50.1")
	data = appendImplicitElement(data, Tag{0x0010, 0x0010}, "Doe^John")
	data = appendImplicitElement(data, Tag{0x0008, 0x0020}, "20230102")

	data = appendTag(data, Tag{0x0008, 0x1110}, true)
	data = append(data, 0xFF, 0xFF, 0xFF, 0xFF)
	data = appendTag(data, tagItem, true)
	data = append(data, 0xFF, 0xFF, 0xFF, 0xFF)
	data = appendImplicitElement(data, Tag{0x0008, 0x1155}, "1.2.3.50.1")
	data = appendImplicitElement(data, Tag{0x0009, 0x1001}, "private-name")
	data = appendTag(data, tagItemDelimitation, true)
	data = append(data, 0, 0, 0, 0)
	data = appendTag(data, tagSequenceDelim, true)
	data = append(data, 0, 0, 0, 0)

	meta := testMeta("1.2.840.10008.1.2", "1.2.3.50.1")
	metaBytes, err := encodeMeta(meta)
	if err != nil {
		t.Fatalf("encode meta: %v", err)
	}
	input := make([]byte, 132)
	copy(input[128:], "DICM")
	input = append(input, metaBytes...)
	input = append(input, data...)

	a := MustNewAnonymizer(WithSecret([]byte("implicit-secret")))
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize implicit dataset: %v", err)
	}
	out := parseForTest(t, output)
	if len(collectPrivateTags(out.data)) != 0 {
		t.Error("private tag in implicit undefined-length sequence was retained")
	}
	seq := out.data.find(Tag{0x0008, 0x1110})
	if seq == nil || len(seq.items) != 1 {
		t.Fatal("undefined-length sequence was not preserved")
	}
	sopUID := valueOf(t, out.data, Tag{0x0008, 0x0018})
	refUID := valueOf(t, seq.items[0], Tag{0x0008, 0x1155})
	if sopUID == "" || sopUID == "1.2.3.50.1" || refUID != sopUID {
		t.Errorf("implicit UID association failed: sop=%q ref=%q", sopUID, refUID)
	}
}

func TestExplicitBigEndianAndDeflatedRoundTrip(t *testing.T) {
	for _, enc := range []fileEncoding{
		{explicit: true, little: false},
		{explicit: true, little: true, deflated: true},
	} {
		name := "big-endian"
		if enc.deflated {
			name = "deflated"
		}
		t.Run(name, func(t *testing.T) {
			input := buildFile(t, baseImageDataset(), enc, "1.2.3.4.5")
			a := MustNewAnonymizer(WithSecret([]byte("encoding-secret")))
			output, err := a.Anonymize(input)
			if err != nil {
				t.Fatalf("anonymize: %v", err)
			}
			out := parseForTest(t, output)
			if out.enc != enc {
				t.Errorf("encoding changed: got %+v want %+v", out.enc, enc)
			}
			if got := valueOf(t, out.data, Tag{0x0010, 0x0010}); got != "" {
				t.Errorf("PatientName retained: %q", got)
			}
			if got := valueOf(t, out.data, Tag{0x0008, 0x0060}); got != "CT" {
				t.Errorf("Modality = %q", got)
			}
		})
	}
}

func TestPreambleCanBePreservedExplicitly(t *testing.T) {
	input := buildFile(t, baseImageDataset(), fileEncoding{explicit: true, little: true}, "1.2.3.4.5")
	for i := 0; i < 128; i++ {
		input[i] = byte(i + 1)
	}
	a := MustNewAnonymizer(
		WithSecret([]byte("preamble-secret")),
		WithPreamblePreserved(true),
	)
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if !bytes.Equal(input[:128], output[:128]) {
		t.Error("preamble was not preserved when requested")
	}
}

func TestParseTag(t *testing.T) {
	for _, spec := range []string{"0009,1001", "(0009,1001)", "00091001"} {
		tag, err := ParseTag(spec)
		if err != nil {
			t.Fatalf("ParseTag(%q): %v", spec, err)
		}
		if tag != (Tag{0x0009, 0x1001}) {
			t.Errorf("ParseTag(%q) = %s", spec, tag)
		}
	}
	if _, err := ParseTag("not-a-tag"); err == nil {
		t.Error("expected invalid tag error")
	}
}

func appendTag(dst []byte, tag Tag, little bool) []byte {
	dst = appendUint16(dst, tag.Group, little)
	dst = appendUint16(dst, tag.Element, little)
	return dst
}

func appendImplicitElement(dst []byte, tag Tag, value string) []byte {
	dst = appendTag(dst, tag, true)
	valueBytes := []byte(value)
	if len(valueBytes)%2 != 0 {
		switch vrForTag(tag) {
		case vrUI, vrOB, vrOW:
			valueBytes = append(valueBytes, 0)
		default:
			valueBytes = append(valueBytes, ' ')
		}
	}
	dst = appendUint32(dst, uint32(len(valueBytes)), true)
	return append(dst, valueBytes...)
}

func TestNoIdentityInWhitelistCase(t *testing.T) {
	ds := &dataset{elements: []*element{
		textElement(Tag{0x0010, 0x0010}, vrPN, "Secret^Name"),
		textElement(Tag{0x0010, 0x0020}, vrLO, "SECRET-ID"),
		textElement(Tag{0x0019, 0x0010}, vrLO, "SAFE CREATOR"),
		textElement(Tag{0x0019, 0x1001}, vrDS, "1.25"),
	}}
	input := buildFile(t, ds, fileEncoding{explicit: true, little: true}, "1.2.3.70.1")
	a := MustNewAnonymizer(
		WithSecret([]byte("whitelist-identity-secret")),
		WithPrivateTagWhitelistSpecs("0019,1001"),
	)
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	if strings.Contains(string(output), "Secret") || strings.Contains(string(output), "SECRET-ID") {
		t.Error("identity survived whitelist-mode anonymization")
	}
}

func TestRTAndVisitValuesAreNotCorruptedByDateOrUIDRules(t *testing.T) {
	ds := &dataset{elements: []*element{
		textElement(Tag{0x0018, 0x1030}, vrLO, "CHEST PROTOCOL"),
		textElement(Tag{0x3006, 0x0002}, vrLO, "Structure Set Label"),
		textElement(Tag{0x3006, 0x0024}, vrUI, "1.2.3.90.1"),
		textElement(Tag{0x3006, 0x0084}, vrIS, "1"),
		textElement(Tag{0x0038, 0x0040}, vrLT, "Discharge diagnosis text"),
		sequenceElement(Tag{0x0038, 0x0050}, &dataset{elements: []*element{
			textElement(Tag{0x0008, 0x0100}, vrSH, "DIAG-CODE"),
		}}),
		textElement(Tag{0x0040, 0x0280}, vrST, "procedure comment"),
	}}
	input := buildFile(t, ds, fileEncoding{explicit: true, little: true}, "1.2.3.90.1")
	a := MustNewAnonymizer(WithSecret([]byte("rt-secret")))
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	out := parseForTest(t, output)
	if got := valueOf(t, out.data, Tag{0x0018, 0x1030}); got != "CHEST PROTOCOL" {
		t.Errorf("ProtocolName should be retained, got %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x3006, 0x0002}); got != "" {
		t.Errorf("StructureSetLabel should be cleared, got %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x3006, 0x0084}); got != "1" {
		t.Errorf("ReferencedROINumber was corrupted: %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x3006, 0x0024}); got == "" || got == "1.2.3.90.1" {
		t.Errorf("ReferencedFrameOfReferenceUID was not remapped: %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x0038, 0x0040}); got != "" {
		t.Errorf("DischargeDiagnosisDescription should be cleared, got %q", got)
	}
	if got := valueOf(t, out.data, Tag{0x0040, 0x0280}); got != "" {
		t.Errorf("procedure comments should be cleared, got %q", got)
	}
	codeSeq := out.data.find(Tag{0x0038, 0x0050})
	if codeSeq == nil || len(codeSeq.items) != 1 {
		t.Fatal("discharge diagnosis code sequence should be preserved")
	}
	if got := valueOf(t, codeSeq.items[0], Tag{0x0008, 0x0100}); got != "DIAG-CODE" {
		t.Errorf("non-identifying code was corrupted: %q", got)
	}
}

func TestFileMetaGroupLengthAndPreambleAreRebuilt(t *testing.T) {
	input := buildFile(t, baseImageDataset(), fileEncoding{explicit: true, little: true}, "1.2.3.4.5")
	a := MustNewAnonymizer(WithSecret([]byte("meta-secret")))
	output, err := a.Anonymize(input)
	if err != nil {
		t.Fatalf("anonymize: %v", err)
	}
	out := parseForTest(t, output)
	groupLength := out.meta.find(Tag{0x0002, 0x0000})
	if groupLength == nil || len(groupLength.value) != 4 {
		t.Fatal("file meta group length missing")
	}
	var rest []*element
	for _, e := range out.meta.elements {
		if e.tag != (Tag{0x0002, 0x0000}) {
			rest = append(rest, e)
		}
	}
	body, err := encodeDataset(&dataset{elements: rest}, true, true)
	if err != nil {
		t.Fatalf("encode meta body: %v", err)
	}
	if got := binary.LittleEndian.Uint32(groupLength.value); got != uint32(len(body)) {
		t.Errorf("file meta group length = %d, want %d", got, len(body))
	}
	if out.meta.find(Tag{0x0002, 0x0016}) != nil {
		t.Error("SourceApplicationEntityTitle was retained")
	}
	if out.meta.elements[0].tag != (Tag{0x0002, 0x0000}) {
		t.Error("file meta group length is not the first meta element")
	}
}

func TestRawDatasetsWithoutPart10Header(t *testing.T) {
	for _, enc := range []fileEncoding{
		{explicit: true, little: true},
		{explicit: false, little: true},
	} {
		name := "implicit"
		if enc.explicit {
			name = "explicit"
		}
		t.Run(name, func(t *testing.T) {
			ds := &dataset{elements: []*element{
				textElement(Tag{0x0008, 0x0016}, vrUI, "1.2.840.10008.5.1.4.1.1.2"),
				textElement(Tag{0x0008, 0x0018}, vrUI, "1.2.3.100.1"),
				textElement(Tag{0x0008, 0x0020}, vrDA, "20240115"),
				textElement(Tag{0x0010, 0x0010}, vrPN, "Raw^Patient"),
				textElement(Tag{0x0010, 0x0020}, vrLO, "RAW-ID"),
				textElement(Tag{0x0009, 0x0010}, vrLO, "RAW PRIVATE"),
				textElement(Tag{0x0009, 0x1001}, vrLO, "Raw^Patient"),
			}}
			input, err := encodeDataset(ds, enc.explicit, enc.little)
			if err != nil {
				t.Fatalf("encode raw dataset: %v", err)
			}
			if bytes.Contains(input, []byte("DICM")) {
				t.Fatal("test fixture unexpectedly contains a Part 10 header")
			}
			a := MustNewAnonymizer(WithSecret([]byte("raw-secret")))
			output, err := a.Anonymize(input)
			if err != nil {
				t.Fatalf("anonymize raw dataset: %v", err)
			}
			if bytes.Contains(output, []byte("DICM")) {
				t.Error("raw dataset was converted to a Part 10 file")
			}
			out := parseForTest(t, output)
			if out.part10 {
				t.Error("output should remain a raw dataset")
			}
			if got := valueOf(t, out.data, Tag{0x0010, 0x0010}); got != "" {
				t.Errorf("raw PatientName retained: %q", got)
			}
			if len(collectPrivateTags(out.data)) != 0 {
				t.Error("raw private tags retained")
			}
			if got := valueOf(t, out.data, Tag{0x0008, 0x0018}); got == "" || got == "1.2.3.100.1" {
				t.Errorf("raw UID not remapped: %q", got)
			}
		})
	}
}
