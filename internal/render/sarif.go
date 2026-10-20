// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/version"
)

// SARIF constants (ADR-0047). The document is SARIF 2.1.0 as accepted by
// GitHub Code Scanning; field choices follow ADR-0047 §2 and §3.
const (
	sarifVersion        = "2.1.0"
	sarifSchemaURI      = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifToolName       = "CommitBrief"
	sarifInformationURI = "https://commitbrief.com"
	sarifRulesDocsURL   = "https://commitbrief.com/docs/1.x/rules"
	// sarifCategory is runAutomationDetails.id, the Code Scanning category.
	// The trailing slash is part of the SARIF convention for category ids.
	sarifCategory = "commitbrief/"
	// sarifFingerprintKey is the partialFingerprints key carrying the
	// ADR-0045 finding fingerprint. primaryLocationLineHash is deliberately
	// left to GitHub's upload-sarif (ADR-0047 §3).
	sarifFingerprintKey = "commitbrief/v1"
)

// Code Scanning categories (runs[0].automationDetails.id) per command. Each
// command owns its own category so that uploading one never closes the alerts
// of another: Code Scanning treats a missing result as fixed within a category.
const (
	// SARIFCategoryReview is the review and diff category (the default).
	SARIFCategoryReview = sarifCategory
	// SARIFCategoryGuard is the `commitbrief guard` category.
	SARIFCategoryGuard = "commitbrief/guard/"
	// SARIFCategoryLeaks is the `commitbrief leaks` category.
	SARIFCategoryLeaks = "commitbrief/leaks/"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool              sarifTool       `json:"tool"`
	AutomationDetails sarifAutomation `json:"automationDetails"`
	Results           []sarifResult   `json:"results"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name            string      `json:"name"`
	SemanticVersion string      `json:"semanticVersion,omitempty"`
	InformationURI  string      `json:"informationUri"`
	Rules           []sarifRule `json:"rules"`
}

type sarifAutomation struct {
	ID string `json:"id"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifRule struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	ShortDescription     sarifText       `json:"shortDescription"`
	FullDescription      sarifText       `json:"fullDescription"`
	Help                 sarifText       `json:"help"`
	DefaultConfiguration sarifRuleConfig `json:"defaultConfiguration"`
	Properties           sarifRuleProps  `json:"properties"`
}

type sarifRuleConfig struct {
	Level string `json:"level"`
}

type sarifRuleProps struct {
	Tags      []string `json:"tags"`
	Precision string   `json:"precision"`
}

type sarifResult struct {
	RuleID              string            `json:"ruleId"`
	RuleIndex           int               `json:"ruleIndex"`
	Level               string            `json:"level"`
	Rank                int               `json:"rank"`
	Message             sarifText         `json:"message"`
	Locations           []sarifLocation   `json:"locations"`
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
	Properties          map[string]any    `json:"properties"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

type sarifArtifactLocation struct {
	URI       string `json:"uri"`
	URIBaseID string `json:"uriBaseId"`
}

type sarifRegion struct {
	StartLine int `json:"startLine"`
	EndLine   int `json:"endLine,omitempty"`
}

// SARIF writes the findings of p as a SARIF 2.1.0 document (ADR-0047).
//
// The caller passes the post-signal-control set -- the same findings --json
// would emit -- and is responsible for refusing a degraded run: a nil
// p.Findings is NOT a valid input here, because an empty results[] tells Code
// Scanning every open alert is fixed (ADR-0047 §4, fail closed). SARIF still
// renders a nil slice as results: [] so the writer itself never panics; the
// refusal lives at the call site, which knows whether the run degraded.
func SARIF(w io.Writer, p Payload) error {
	return writeSARIF(w, p.Findings, version.Version)
}

// SARIFWithCategory is SARIF for the commands that own their own Code
// Scanning category (guard, leaks). extraProps, when non-nil, is parallel to
// findings: extraProps[i] is merged into findings[i]'s properties bag, so a
// caller can attach command-specific keys without widening Finding. A key in
// extraProps never overrides the renderer's own keys.
func SARIFWithCategory(w io.Writer, findings []Finding, category string, extraProps []map[string]any) error {
	return writeSARIFCategory(w, findings, version.Version, category, extraProps)
}

// writeSARIF is SARIF with the tool version injected, so tests can pin it.
func writeSARIF(w io.Writer, findings []Finding, toolVersion string) error {
	return writeSARIFCategory(w, findings, toolVersion, sarifCategory, nil)
}

// writeSARIFCategory is the shared body: tool version, category and optional
// per-finding extra properties all injected.
func writeSARIFCategory(w io.Writer, findings []Finding, toolVersion, category string, extraProps []map[string]any) error {
	taxonomy := rules.Taxonomy()
	ruleIndex := make(map[string]int, len(taxonomy))
	descriptors := make([]sarifRule, len(taxonomy))
	for i, r := range taxonomy {
		ruleIndex[r.ID] = i
		descriptors[i] = sarifRuleFor(r)
	}

	results := make([]sarifResult, 0, len(findings))
	for i, f := range findings {
		res := sarifResultFor(f, ruleIndex)
		if i < len(extraProps) {
			for k, v := range extraProps[i] {
				if _, taken := res.Properties[k]; !taken {
					res.Properties[k] = v
				}
			}
		}
		results = append(results, res)
	}

	driver := sarifDriver{
		Name:           sarifToolName,
		InformationURI: sarifInformationURI,
		Rules:          descriptors,
	}
	// A bare dev build has no semantic version; leave the field out rather
	// than publish a string that is not one.
	if v := strings.TrimPrefix(toolVersion, "v"); v != "" && v != "dev" {
		driver.SemanticVersion = v
	}

	doc := sarifLog{
		Schema:  sarifSchemaURI,
		Version: sarifVersion,
		Runs: []sarifRun{{
			Tool:              sarifTool{Driver: driver},
			AutomationDetails: sarifAutomation{ID: category},
			Results:           results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("render: encode sarif: %w", err)
	}
	return nil
}

func sarifRuleFor(r rules.Rule) sarifRule {
	tags := []string{r.Category()}
	if r.Category() == "sec" {
		tags = append(tags, "security")
	}
	return sarifRule{
		ID:               r.ID,
		Name:             r.ID,
		ShortDescription: sarifText{Text: r.Definition},
		FullDescription:  sarifText{Text: r.Definition},
		Help:             sarifText{Text: r.Definition + "\n\n" + sarifRulesDocsURL},
		DefaultConfiguration: sarifRuleConfig{
			Level: string(r.Level),
		},
		Properties: sarifRuleProps{
			Tags: tags,
			// LLM-based detection: "medium" for every rule, including the
			// deterministic test.flaky and sec.hardcoded-secret. A per-rule
			// precision waits for a measurement (ADR-0047 §2).
			Precision: "medium",
		},
	}
}

func sarifResultFor(f Finding, ruleIndex map[string]int) sarifResult {
	// Normalise defensively: stampIdentity already did on the review path,
	// but a finding from any other route may carry an empty or aliased id.
	id, _ := rules.NormaliseRuleID(f.RuleID)

	props := map[string]any{
		"commitbrief/severity": string(f.Severity),
	}
	region := sarifRegion{StartLine: f.Line}
	if f.Line <= 0 {
		// GitHub needs a line; upload-sarif computes no fingerprint for a
		// result without one. A file-level finding anchors on line 1 and
		// says so.
		region.StartLine = 1
		props["commitbrief/file-level"] = true
	} else if f.LineEnd > f.Line {
		region.EndLine = f.LineEnd
	}

	res := sarifResult{
		RuleID:    id,
		RuleIndex: ruleIndex[id],
		Level:     sarifLevel(f.Severity),
		Rank:      sarifRank(f.Severity),
		Message:   sarifText{Text: sarifMessage(f)},
		Locations: []sarifLocation{{
			PhysicalLocation: sarifPhysicalLocation{
				ArtifactLocation: sarifArtifactLocation{
					URI:       sarifURI(f.File),
					URIBaseID: "%SRCROOT%",
				},
				Region: region,
			},
		}},
		Properties: props,
	}
	if f.Fingerprint != "" {
		res.PartialFingerprints = map[string]string{sarifFingerprintKey: f.Fingerprint}
	}
	return res
}

// sarifURI renders a repo-relative path as a SARIF uri-reference: canonical
// forward slashes, then every segment percent-encoded so '#', '?', '%' and
// spaces cannot be read as a fragment, a query or a separator. ':' is escaped
// too, because a relative reference whose first segment holds one ("C:/x")
// would parse as a URI scheme. The '/' separators stay literal.
func sarifURI(file string) string {
	segments := strings.Split(diff.CanonicalPath(file), "/")
	for i, seg := range segments {
		segments[i] = strings.ReplaceAll(url.PathEscape(seg), ":", "%3A")
	}
	return strings.Join(segments, "/")
}

// sarifLevel maps a CommitBrief severity to a SARIF level (ADR-0047 §3).
// An unrecognised severity cannot occur after parsing; it degrades to the
// lowest level instead of inventing a louder one.
func sarifLevel(s Severity) string {
	switch s {
	case SeverityCritical, SeverityHigh:
		return "error"
	case SeverityMedium:
		return "warning"
	default:
		return "note"
	}
}

// sarifRank is the ordinal encoding of a severity on SARIF's 0-100 rank
// scale (ADR-0047 §3). It is an ordering, not a calibrated confidence, and
// GitHub ignores it; the exact severity travels in
// properties["commitbrief/severity"].
func sarifRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 90
	case SeverityHigh:
		return 70
	case SeverityMedium:
		return 50
	case SeverityLow:
		return 30
	default:
		return 10
	}
}

// sarifMessage is "Title — Description", plus "\nSuggestion: ..." when the
// finding carries one.
func sarifMessage(f Finding) string {
	title := strings.TrimSpace(f.Title)
	desc := strings.TrimSpace(f.Description)
	var msg string
	switch {
	case title != "" && desc != "":
		msg = title + " — " + desc
	case title != "":
		msg = title
	default:
		msg = desc
	}
	if s := strings.TrimSpace(f.Suggestion); s != "" {
		msg += "\nSuggestion: " + s
	}
	return msg
}
