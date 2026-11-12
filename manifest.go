package netsec

import (
	"encoding/xml"
	"fmt"
	"io"
)

// androidNS is the namespace used by android:* attributes in a text
// AndroidManifest.xml.
const androidNS = "http://schemas.android.com/apk/res/android"

// Manifest mirrors the security-relevant subset of an AndroidManifest.xml.
// Instances can be built programmatically or produced with ParseManifest.
type Manifest struct {
	Package     string
	TargetSDK   int
	Permissions []Permission
	Application Application
}

// Permission is a permission declared by a <permission> element.
type Permission struct {
	Name            string
	ProtectionLevel string
}

// Application holds the application-level permission default and components.
type Application struct {
	Permission      string
	Activities      []Activity
	ActivityAliases []ActivityAlias
	Services        []Service
	Providers       []Provider
}

// ComponentKind identifies the kind of an exported component.
type ComponentKind string

const (
	KindActivity      ComponentKind = "activity"
	KindActivityAlias ComponentKind = "activity-alias"
	KindService       ComponentKind = "service"
	KindProvider      ComponentKind = "provider"
)

// Component is the shared representation of Activity, Service and Provider.
// A nil Exported means android:exported is absent and platform default rules
// apply. HasFilter reports whether the component declares an intent-filter.
type Component struct {
	Kind       ComponentKind
	Name       string
	Exported   *bool
	HasFilter  bool
	Permission string
}

// Activity is an <activity> element.
type Activity struct {
	Component
}

// Service is a <service> element.
type Service struct {
	Component
}

// Provider is a <provider> element.
type Provider struct {
	Component
}

// ActivityAlias is an <activity-alias> element. Its exposure follows the
// targetActivity it points at; its own android:permission, when present,
// still overrides the application-level permission.
type ActivityAlias struct {
	Name           string
	TargetActivity string
	Exported       *bool
	HasFilter      bool
	Permission     string
}

type xmlManifest struct {
	XMLName     xml.Name        `xml:"manifest"`
	Package     string          `xml:"package,attr"`
	UsesSDK     *xmlUsesSDK     `xml:"uses-sdk"`
	Permissions []xmlPermission `xml:"permission"`
	Application xmlApplication  `xml:"application"`
}

type xmlUsesSDK struct {
	TargetSDK *int `xml:"http://schemas.android.com/apk/res/android targetSdkVersion,attr"`
}

type xmlPermission struct {
	Name            string `xml:"http://schemas.android.com/apk/res/android name,attr"`
	ProtectionLevel string `xml:"http://schemas.android.com/apk/res/android protectionLevel,attr"`
}

type xmlApplication struct {
	Permission      string         `xml:"http://schemas.android.com/apk/res/android permission,attr"`
	Activities      []xmlComponent `xml:"activity"`
	ActivityAliases []xmlAlias     `xml:"activity-alias"`
	Services        []xmlComponent `xml:"service"`
	Providers       []xmlComponent `xml:"provider"`
}

type xmlIntentFilter struct {
	XMLName xml.Name `xml:"intent-filter"`
}

type xmlComponent struct {
	Name       string            `xml:"http://schemas.android.com/apk/res/android name,attr"`
	Exported   *bool             `xml:"http://schemas.android.com/apk/res/android exported,attr"`
	Permission string            `xml:"http://schemas.android.com/apk/res/android permission,attr"`
	Filters    []xmlIntentFilter `xml:"intent-filter"`
}

type xmlAlias struct {
	Name           string            `xml:"http://schemas.android.com/apk/res/android name,attr"`
	TargetActivity string            `xml:"http://schemas.android.com/apk/res/android targetActivity,attr"`
	Exported       *bool             `xml:"http://schemas.android.com/apk/res/android exported,attr"`
	Permission     string            `xml:"http://schemas.android.com/apk/res/android permission,attr"`
	Filters        []xmlIntentFilter `xml:"intent-filter"`
}

func (c xmlComponent) component(kind ComponentKind) Component {
	return Component{
		Kind:       kind,
		Name:       c.Name,
		Exported:   c.Exported,
		HasFilter:  len(c.Filters) > 0,
		Permission: c.Permission,
	}
}

// ParseManifest decodes the security-relevant subset of a text
// AndroidManifest.xml. A missing uses-sdk leaves TargetSDK at zero, which
// Analyze treats like a legacy platform version.
func ParseManifest(r io.Reader) (*Manifest, error) {
	var raw xmlManifest
	if err := xml.NewDecoder(r).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse android manifest: %w", err)
	}

	m := &Manifest{
		Package:     raw.Package,
		Permissions: make([]Permission, 0, len(raw.Permissions)),
	}
	if raw.UsesSDK != nil && raw.UsesSDK.TargetSDK != nil {
		m.TargetSDK = *raw.UsesSDK.TargetSDK
	}
	for _, p := range raw.Permissions {
		m.Permissions = append(m.Permissions, Permission{
			Name:            p.Name,
			ProtectionLevel: p.ProtectionLevel,
		})
	}

	m.Application.Permission = raw.Application.Permission
	for _, a := range raw.Application.Activities {
		m.Application.Activities = append(m.Application.Activities, Activity{a.component(KindActivity)})
	}
	for _, s := range raw.Application.Services {
		m.Application.Services = append(m.Application.Services, Service{s.component(KindService)})
	}
	for _, p := range raw.Application.Providers {
		m.Application.Providers = append(m.Application.Providers, Provider{p.component(KindProvider)})
	}
	for _, a := range raw.Application.ActivityAliases {
		m.Application.ActivityAliases = append(m.Application.ActivityAliases, ActivityAlias{
			Name:           a.Name,
			TargetActivity: a.TargetActivity,
			Exported:       a.Exported,
			HasFilter:      len(a.Filters) > 0,
			Permission:     a.Permission,
		})
	}
	return m, nil
}
