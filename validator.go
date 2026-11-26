package rpki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"path"
	"slices"
	"strings"
	"time"
)

const DefaultMaxDepth = 16

var (
	ErrSIAPointer  = errors.New("rpki: invalid SIA pointer")
	ErrSIACycle    = errors.New("rpki: cyclic SIA pointer")
	ErrDepth       = errors.New("rpki: delegation chain too deep")
	ErrPublication = errors.New("rpki: publication point invalid")
)

// Options configures offline top-down validation.
type Options struct {
	Now                  func() time.Time
	States               StateStore
	MaxDepth             int
	FutureSkew           time.Duration
	AllowUnlistedFiles   bool
	RejectUnknownObjects bool
	AllowNonP256         bool
	DisableStateSave     bool
	MapSIA               func(uris []string) (string, error)
}

func (o Options) defaults() Options {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.FutureSkew == 0 {
		o.FutureSkew = 5 * time.Minute
	}
	if o.MapSIA == nil {
		o.MapSIA = DefaultSIAMapper
	}
	return o
}

func (o Options) verifyOptions() VerifyOptions {
	return VerifyOptions{Now: o.Now(), RequireP256: !o.AllowNonP256}
}

// Validator performs offline repository validation.
type Validator struct {
	Repo    Repository
	Options Options
}

func NewValidator(repo Repository, opts Options) *Validator {
	return &Validator{Repo: repo, Options: opts.defaults()}
}

// ObjectReport describes a semantically checked signed object or certificate.
type ObjectReport struct {
	Path       string
	Kind       string
	SignerKey  string
	CustomerAS uint64
	Valid      bool
	Err        error
}

// CAReport is the validation state of one CA publication point/subtree.
type CAReport struct {
	Key         string
	Name        string
	Directory   string
	Depth       int
	Certificate *CertInfo
	Manifest    *Manifest
	Watermark   Watermark
	CRL         *ValidatedCRL
	Files       []string
	Objects     []ObjectReport
	Children    []*CAReport
	Valid       bool
	Err         error
}

// ValidateTAL validates the complete tree rooted at the pinned TAL. A non-nil
// report is returned together with an error aggregate; invalid child subtrees
// are present as invalid reports and are never recursively trusted.
func (v *Validator) ValidateTAL(ctx context.Context, tal TAL) (*CAReport, error) {
	if v.Repo == nil {
		return nil, errors.New("rpki: validator has no Repository")
	}
	talCert, talPath, err := v.loadTALCertificate(tal)
	if err != nil {
		return nil, err
	}
	if err := VerifyTrustAnchor(tal, talCert, v.Options.verifyOptions()); err != nil {
		return &CAReport{Valid: false, Certificate: talCert, Err: err}, err
	}
	dir, err := v.Options.MapSIA(talCert.CARepositories)
	if err != nil {
		err = fmt.Errorf("%w: trust anchor %s: %v", ErrSIAPointer, tal.Name, err)
		return &CAReport{Valid: false, Certificate: talCert, Err: err}, err
	}
	_ = talPath
	report, err := v.validateCA(ctx, talCert, nil, dir, 0, []string{dir})
	if err != nil {
		return report, err
	}
	if report != nil && !report.Valid {
		return report, report.Err
	}
	return report, nil
}

func (v *Validator) loadTALCertificate(tal TAL) (*CertInfo, string, error) {
	var errs []error
	for _, uri := range tal.URIs {
		name, err := MapRSyncURI(uri)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		der, err := v.Repo.Open(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		cert, err := ParseCertificate(der)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return cert, name, nil
	}
	return nil, "", fmt.Errorf("rpki: could not load TAL certificate %s: %w", tal.Name, errors.Join(errs...))
}

func (v *Validator) validateCA(
	ctx context.Context,
	cert *CertInfo,
	parent *CertInfo,
	dir string,
	depth int,
	stack []string,
) (*CAReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report := &CAReport{
		Key:         cert.StateKey(),
		Name:        cert.X509.Subject.CommonName,
		Directory:   dir,
		Depth:       depth,
		Certificate: cert,
		Valid:       false,
	}
	fail := func(err error) (*CAReport, error) {
		report.Err = err
		report.Valid = false
		return report, err
	}

	if depth > v.Options.MaxDepth {
		return fail(fmt.Errorf("%w: depth %d > max %d at %s", ErrDepth, depth, v.Options.MaxDepth, dir))
	}
	if countOccurrences(stack, dir) > 1 {
		return fail(fmt.Errorf("%w: directory %q in SIA path %s", ErrSIACycle, dir, strings.Join(stack, " -> ")))
	}

	manifestPath, manifestDER, err := v.findManifest(dir)
	if err != nil {
		return fail(err)
	}
	cms, err := ParseAndVerifyCMS(manifestDER)
	if err != nil {
		return fail(fmt.Errorf("%w: %s: %w", ErrPublication, manifestPath, err))
	}
	if !cms.ContentType.Equal(oidManifest) {
		return fail(fmt.Errorf("%w: %s CMS content type %s is not manifest", ErrManifest, manifestPath, cms.ContentType))
	}
	manifest, err := ParseManifest(cms.Content)
	if err != nil {
		return fail(err)
	}
	report.Manifest = manifest

	manifestHash := sha256Hex(manifestDER)
	watermark := Watermark{
		CAKey:          report.Key,
		CAName:         report.Name,
		RepositoryDir:  dir,
		ManifestNumber: new(big.Int).Set(manifest.ManifestNumber),
		ThisUpdate:     manifest.ThisUpdate,
		NextUpdate:     manifest.NextUpdate,
		ManifestSHA256: manifestHash,
		LastSuccess:    v.Options.Now(),
	}
	report.Watermark = watermark

	if v.Options.States != nil {
		old, ok, err := v.Options.States.LoadWatermark(ctx, report.Key)
		if err != nil {
			return fail(err)
		}
		if ok {
			if err := CheckWatermark(old, watermark); err != nil {
				return fail(err)
			}
		}
	}
	if err := manifest.CheckFreshness(v.Options.Now(), v.Options.FutureSkew); err != nil {
		return fail(err)
	}

	checkedFiles, err := manifest.VerifyHashes(v.Repo, dir, path.Base(manifestPath), !v.Options.AllowUnlistedFiles)
	if err != nil {
		return fail(err)
	}
	report.Files = checkedFiles

	crlPath, crlDER, err := v.findCRL(manifest, dir)
	if err != nil {
		return fail(err)
	}
	crl, err := ParseAndVerifyCRL(crlDER, cert, v.Options.Now())
	if err != nil {
		return fail(err)
	}
	report.CRL = crl

	manifestEE := cms.Signer
	if err := VerifyEECertificate(manifestEE, cert, v.Options.verifyOptions()); err != nil {
		return fail(fmt.Errorf("manifest EE invalid: %w", err))
	}
	if err := crl.RequireNotRevoked(manifestEE); err != nil {
		return fail(fmt.Errorf("manifest EE rejected: %w", err))
	}
	report.Objects = append(report.Objects, ObjectReport{
		Path:      manifestPath,
		Kind:      "manifest",
		SignerKey: manifestEE.StateKey(),
		Valid:     true,
	})

	var childErrs []error
	for _, fh := range manifest.FileHashes {
		fullPath, err := JoinRepositoryPath(dir, fh.Name)
		if err != nil {
			return fail(err)
		}
		lower := strings.ToLower(fh.Name)
		switch {
		case strings.HasSuffix(lower, ".crl"):
			if fullPath != crlPath {
				return fail(fmt.Errorf("%w: unexpected CRL %s", ErrCRL, fullPath))
			}
		case strings.HasSuffix(lower, ".cer"):
			childReport, childErr := v.validateCertificateFile(ctx, fullPath, cert, crl, depth, stack)
			if childReport != nil && childReport.Certificate != nil {
				if childReport.Certificate.X509.IsCA && (childReport.Directory != "" || childReport.Err != nil) {
					report.Children = append(report.Children, childReport)
				}
				report.Objects = append(report.Objects, ObjectReport{
					Path:      fullPath,
					Kind:      certificateKind(childReport.Certificate),
					SignerKey: cert.StateKey(),
					Valid:     childErr == nil,
					Err:       childErr,
				})
			}
			if childErr != nil {
				childErrs = append(childErrs, childErr)
			}
		case strings.HasSuffix(lower, ".roa"):
			objReport, err := v.validateROAFile(fullPath, cert, crl)
			report.Objects = append(report.Objects, objReport)
			if err != nil {
				childErrs = append(childErrs, err)
			}
		case strings.HasSuffix(lower, ".aspa"):
			objReport, err := v.validateASPAFile(fullPath, cert, crl)
			report.Objects = append(report.Objects, objReport)
			if err != nil {
				childErrs = append(childErrs, err)
			}
		default:
			if v.Options.RejectUnknownObjects {
				err := fmt.Errorf("%w: unknown signed object type %s", ErrSignedObject, fh.Name)
				report.Objects = append(report.Objects, ObjectReport{Path: fullPath, Kind: "unknown", Valid: false, Err: err})
				childErrs = append(childErrs, err)
			} else {
				report.Objects = append(report.Objects, ObjectReport{Path: fullPath, Kind: "ignored-unknown-hash-checked", Valid: true})
			}
		}
	}

	if v.Options.States != nil && !v.Options.DisableStateSave {
		if err := v.Options.States.SaveWatermark(ctx, watermark); err != nil {
			return fail(err)
		}
	}

	if len(childErrs) > 0 {
		report.Valid = false
		report.Err = errors.Join(childErrs...)
		return report, report.Err
	}
	report.Valid = true
	return report, nil
}

func (v *Validator) validateCertificateFile(
	ctx context.Context,
	fullPath string,
	issuer *CertInfo,
	crl *ValidatedCRL,
	depth int,
	stack []string,
) (*CAReport, error) {
	der, err := v.Repo.Open(fullPath)
	if err != nil {
		return nil, err
	}
	cert, err := ParseCertificate(der)
	if err != nil {
		return &CAReport{Valid: false, Err: err}, err
	}
	sameIssuerKey := bytes.Equal(cert.X509.RawSubjectPublicKeyInfo, issuer.X509.RawSubjectPublicKeyInfo)
	if cert.X509.IsCA && hasKeyUsage(cert.X509.KeyUsage, x509.KeyUsageCertSign) {
		if sameIssuerKey {
			if err := VerifySubordinate(cert, issuer, v.Options.verifyOptions()); err != nil {
				return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: err}, err
			}
			if err := crl.RequireNotRevoked(cert); err != nil {
				return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: err}, err
			}
			return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: true}, nil
		}
		if err := VerifySubordinate(cert, issuer, v.Options.verifyOptions()); err != nil {
			report := &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: err}
			return report, err
		}
		if err := crl.RequireNotRevoked(cert); err != nil {
			wrapped := fmt.Errorf("subordinate CA rejected by CRL %s: %w", fullPath, err)
			report := &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: wrapped}
			return report, wrapped
		}
		dir, err := v.Options.MapSIA(cert.CARepositories)
		if err != nil {
			wrapped := fmt.Errorf("%w: subordinate %s: %v", ErrSIAPointer, fullPath, err)
			report := &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: wrapped}
			return report, wrapped
		}
		if slices.Contains(stack, dir) {
			wrapped := fmt.Errorf("%w: %s points back to %s", ErrSIACycle, fullPath, dir)
			report := &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Directory: dir, Valid: false, Err: wrapped}
			return report, wrapped
		}
		return v.validateCA(ctx, cert, issuer, dir, depth+1, append(append([]string(nil), stack...), dir))
	}

	if err := VerifyEECertificate(cert, issuer, v.Options.verifyOptions()); err != nil {
		return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: err}, err
	}
	if err := crl.RequireNotRevoked(cert); err != nil {
		wrapped := fmt.Errorf("EE certificate %s rejected: %w", fullPath, err)
		return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: false, Err: wrapped}, wrapped
	}
	return &CAReport{Key: cert.StateKey(), Name: cert.X509.Subject.CommonName, Certificate: cert, Valid: true}, nil
}

func (v *Validator) validateROAFile(fullPath string, issuer *CertInfo, crl *ValidatedCRL) (ObjectReport, error) {
	report := ObjectReport{Path: fullPath, Kind: "roa"}
	der, err := v.Repo.Open(fullPath)
	if err != nil {
		report.Err = err
		return report, err
	}
	cms, err := ParseAndVerifyCMS(der)
	if err != nil {
		report.Err = err
		return report, err
	}
	if !cms.ContentType.Equal(oidROA) {
		err := fmt.Errorf("%w: %s content type %s is not ROA", ErrSignedObject, fullPath, cms.ContentType)
		report.Err = err
		return report, err
	}
	if err := VerifyEECertificate(cms.Signer, issuer, v.Options.verifyOptions()); err != nil {
		err = fmt.Errorf("ROA EE invalid: %w", err)
		report.Err = err
		return report, err
	}
	if err := crl.RequireNotRevoked(cms.Signer); err != nil {
		err = fmt.Errorf("ROA EE rejected: %w", err)
		report.Err = err
		return report, err
	}
	roa, err := ParseROA(cms.Content)
	if err != nil {
		report.Err = err
		return report, err
	}
	if err := roa.ValidateAgainstEE(cms.Signer); err != nil {
		report.Err = err
		return report, err
	}
	report.SignerKey = cms.Signer.StateKey()
	report.CustomerAS = roa.ASN
	report.Valid = true
	return report, nil
}

func (v *Validator) validateASPAFile(fullPath string, issuer *CertInfo, crl *ValidatedCRL) (ObjectReport, error) {
	report := ObjectReport{Path: fullPath, Kind: "aspa"}
	der, err := v.Repo.Open(fullPath)
	if err != nil {
		report.Err = err
		return report, err
	}
	cms, err := ParseAndVerifyCMS(der)
	if err != nil {
		report.Err = err
		return report, err
	}
	if !cms.ContentType.Equal(oidASPA) {
		err := fmt.Errorf("%w: %s content type %s is not ASPA", ErrSignedObject, fullPath, cms.ContentType)
		report.Err = err
		return report, err
	}
	if err := VerifyEECertificate(cms.Signer, issuer, v.Options.verifyOptions()); err != nil {
		err = fmt.Errorf("ASPA EE invalid: %w", err)
		report.Err = err
		return report, err
	}
	if err := crl.RequireNotRevoked(cms.Signer); err != nil {
		err = fmt.Errorf("ASPA EE rejected: %w", err)
		report.Err = err
		return report, err
	}
	aspa, err := ParseASPA(cms.Content)
	if err != nil {
		report.Err = err
		return report, err
	}
	if err := aspa.ValidateAgainstEE(cms.Signer); err != nil {
		report.Err = err
		return report, err
	}
	report.SignerKey = cms.Signer.StateKey()
	report.CustomerAS = aspa.CustomerAS
	report.Valid = true
	return report, nil
}

func (v *Validator) findManifest(dir string) (string, []byte, error) {
	entries, err := v.Repo.List(dir)
	if err != nil {
		return "", nil, fmt.Errorf("%w: list %s: %v", ErrPublication, dir, err)
	}
	var matches []string
	for _, entry := range entries {
		if strings.HasSuffix(strings.ToLower(entry), ".mft") {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return "", nil, fmt.Errorf("%w: no .mft in %s", ErrPublication, dir)
	}
	if len(matches) > 1 {
		return "", nil, fmt.Errorf("%w: multiple manifests in %s: %v", ErrPublication, dir, matches)
	}
	data, err := v.Repo.Open(matches[0])
	if err != nil {
		return "", nil, fmt.Errorf("%w: manifest unreadable: %v", ErrPublication, err)
	}
	return matches[0], data, nil
}

func (v *Validator) findCRL(manifest *Manifest, dir string) (string, []byte, error) {
	var names []string
	for _, fh := range manifest.FileHashes {
		if strings.HasSuffix(strings.ToLower(fh.Name), ".crl") {
			names = append(names, fh.Name)
		}
	}
	if len(names) == 0 {
		return "", nil, fmt.Errorf("%w: manifest lists no CRL in %s", ErrPublication, dir)
	}
	if len(names) > 1 {
		return "", nil, fmt.Errorf("%w: manifest lists multiple CRLs: %v", ErrPublication, names)
	}
	full, err := JoinRepositoryPath(dir, names[0])
	if err != nil {
		return "", nil, err
	}
	data, err := v.Repo.Open(full)
	if err != nil {
		return "", nil, fmt.Errorf("%w: CRL missing despite manifest: %v", ErrPublication, err)
	}
	return full, data, nil
}

func DefaultSIAMapper(uris []string) (string, error) {
	var errs []error
	for _, uri := range uris {
		dir, err := MapRSyncURI(uri)
		if err == nil {
			return dir, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("no caRepository SIA URI")
	}
	return "", errors.Join(errs...)
}

func countOccurrences(stack []string, target string) int {
	count := 0
	for _, item := range stack {
		if item == target {
			count++
		}
	}
	return count
}

func certificateKind(cert *CertInfo) string {
	if cert == nil || cert.X509 == nil {
		return "certificate"
	}
	if cert.X509.IsCA {
		return "ca-certificate"
	}
	return "ee-certificate"
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
