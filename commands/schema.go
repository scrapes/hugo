// Copyright 2024 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package commands

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var _ cmder = (*schemaCmd)(nil)

type schemaCmd struct {
	*baseCmd
}

type TemplateVariable struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Path        string `json:"path,omitempty"`
}

type TemplateSchema struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Type        string             `json:"type"`
	Properties  map[string]interface{} `json:"properties"`
	Required    []string           `json:"required"`
	Variables   []TemplateVariable `json:"variables"`
}

func newSchemaCmd() *schemaCmd {
	cc := &schemaCmd{}

	cc.baseCmd = newBaseCmd(&cobra.Command{
		Use:   "schema [flags] <template-path>",
		Short: "Generate JSON schema from Hugo template",
		Long: `Generate a JSON schema from a Hugo template file that describes
the variables and data structure needed to render the template.

This command analyzes the template file and extracts:
- Template variables (e.g., {{.Title}}, {{.Content}})
- Field access patterns (e.g., {{.Site.Title}})
- Function calls and their parameters
- Control structures (if, range, with)

The output is a JSON schema that can be used to validate data
before rendering the template or to generate documentation.

Examples:
  hugo schema layouts/index.html
  hugo schema layouts/partials/header.html
  hugo schema layouts/_default/single.html`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return cc.runSchema(args[0])
		},
	})

	return cc
}

func (c *schemaCmd) runSchema(templatePath string) error {
	// Check if template file exists
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return fmt.Errorf("template file does not exist: %s", templatePath)
	}

	// Read template content
	content, err := ioutil.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %v", err)
	}

	// Extract variables and generate schema
	schema := c.generateSchema(string(content), templatePath)

	// Output JSON schema
	jsonOutput, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	fmt.Println(string(jsonOutput))
	return nil
}

func (c *schemaCmd) generateSchema(templateContent, templatePath string) *TemplateSchema {
	schema := &TemplateSchema{
		Title:       fmt.Sprintf("Schema for %s", filepath.Base(templatePath)),
		Description: fmt.Sprintf("JSON schema for Hugo template: %s", templatePath),
		Type:        "object",
		Properties:  make(map[string]interface{}),
		Required:    []string{},
		Variables:   []TemplateVariable{},
	}

	// Track all variables found
	variables := make(map[string]TemplateVariable)

	// Extract variables using regex
	c.extractVariablesFromContent(templateContent, variables)

	// Convert variables to schema properties
	for _, variable := range variables {
		schema.Variables = append(schema.Variables, variable)
		
		// Add to properties
		propType := c.inferType(variable.Name, variable.Path)
		schema.Properties[variable.Name] = map[string]interface{}{
			"type":        propType,
			"description": variable.Description,
		}

		if variable.Required {
			schema.Required = append(schema.Required, variable.Name)
		}
	}

	// Sort variables for consistent output
	sort.Slice(schema.Variables, func(i, j int) bool {
		return schema.Variables[i].Name < schema.Variables[j].Name
	})

	// Sort required fields
	sort.Strings(schema.Required)

	return schema
}

func (c *schemaCmd) extractVariablesFromContent(content string, variables map[string]TemplateVariable) {
	// Regex patterns for different template constructs
	patterns := map[string]*regexp.Regexp{
		// Field access: .Title, .Site.Title, .Params.something
		"fieldAccess": regexp.MustCompile(`\{\{\s*\.([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`),
		// Variable references: $title, $site
		"variableRef": regexp.MustCompile(`\{\{\s*\$([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`),
		// Variable declarations: $title := .Title
		"variableDecl": regexp.MustCompile(`\{\{\s*\$([a-zA-Z_][a-zA-Z0-9_.]*)\s*:=\s*[^}]+\}\}`),
		// Function calls with field access: {{ .Title | upper }}
		"functionWithField": regexp.MustCompile(`\{\{\s*\.([a-zA-Z_][a-zA-Z0-9_.]*)\s*\|[^}]+\}\}`),
		// Range over fields: {{ range .Pages }}
		"rangeField": regexp.MustCompile(`\{\{\s*range\s+\.([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`),
		// With field: {{ with .Site }}
		"withField": regexp.MustCompile(`\{\{\s*with\s+\.([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`),
		// If field: {{ if .Title }}
		"ifField": regexp.MustCompile(`\{\{\s*if\s+\.([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`),
	}

	// Extract field access patterns
	for _, match := range patterns["fieldAccess"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fieldName := match[1]
			variables[fieldName] = TemplateVariable{
				Name:        fieldName,
				Type:        c.inferType(fieldName, ""),
				Description: c.getFieldDescription(fieldName),
				Required:    true,
				Path:        "",
			}
		}
	}

	// Extract variable references
	for _, match := range patterns["variableRef"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			varName := match[1]
			variables[varName] = TemplateVariable{
				Name:     varName,
				Type:     "any",
				Required: false,
				Path:     "",
			}
		}
	}

	// Extract variable declarations
	for _, match := range patterns["variableDecl"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			varName := match[1]
			variables[varName] = TemplateVariable{
				Name:     varName,
				Type:     "any",
				Required: false,
				Path:     "",
			}
		}
	}

	// Extract function calls with field access
	for _, match := range patterns["functionWithField"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fieldName := match[1]
			variables[fieldName] = TemplateVariable{
				Name:        fieldName,
				Type:        c.inferType(fieldName, ""),
				Description: c.getFieldDescription(fieldName),
				Required:    true,
				Path:        "",
			}
		}
	}

	// Extract range fields
	for _, match := range patterns["rangeField"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fieldName := match[1]
			variables[fieldName] = TemplateVariable{
				Name:        fieldName,
				Type:        "array",
				Description: c.getFieldDescription(fieldName),
				Required:    true,
				Path:        "",
			}
		}
	}

	// Extract with fields
	for _, match := range patterns["withField"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fieldName := match[1]
			variables[fieldName] = TemplateVariable{
				Name:        fieldName,
				Type:        c.inferType(fieldName, ""),
				Description: c.getFieldDescription(fieldName),
				Required:    true,
				Path:        "",
			}
		}
	}

	// Extract if fields
	for _, match := range patterns["ifField"].FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fieldName := match[1]
			variables[fieldName] = TemplateVariable{
				Name:        fieldName,
				Type:        c.inferType(fieldName, ""),
				Description: c.getFieldDescription(fieldName),
				Required:    true,
				Path:        "",
			}
		}
	}
}

func (c *schemaCmd) inferType(fieldName, path string) string {
	// Common Hugo field types
	fieldTypeMap := map[string]string{
		"Title":       "string",
		"Content":     "string",
		"Summary":     "string",
		"Description": "string",
		"Date":        "string",
		"Lastmod":     "string",
		"PublishDate": "string",
		"ExpiryDate":  "string",
		"URL":         "string",
		"Permalink":   "string",
		"RelPermalink": "string",
		"Type":        "string",
		"Kind":        "string",
		"Section":     "string",
		"Layout":      "string",
		"Lang":        "string",
		"LangDir":     "string",
		"Dir":         "string",
		"Weight":      "number",
		"WordCount":   "number",
		"ReadingTime": "number",
		"FuzzyWordCount": "number",
		"Draft":       "boolean",
		"Publish":     "boolean",
		"IsHome":      "boolean",
		"IsPage":      "boolean",
		"IsSection":   "boolean",
		"IsNode":      "boolean",
		"IsMenuCurrent": "boolean",
		"HasMenuCurrent": "boolean",
		"Params":      "object",
		"Resources":   "array",
		"Pages":       "array",
		"RegularPages": "array",
		"AllTranslations": "array",
		"Translations": "array",
		"AlternativeOutputFormats": "array",
		"OutputFormats": "array",
		"File":         "object",
		"GitInfo":      "object",
		"Site":         "object",
		"Taxonomies":   "object",
		"Data":         "object",
		"Menus":        "object",
		"Home":         "object",
		"Next":         "object",
		"Prev":         "object",
		"NextInSection": "object",
		"PrevInSection": "object",
		"Parent":       "object",
		"CurrentSection": "object",
		"FirstSection": "object",
		"InSection":    "object",
		"Ancestors":    "array",
		"Current":      "object",
		"CurrentPage":  "object",
		"Paginator":    "object",
		"Scratch":      "object",
		"Ref":          "string",
		"RelRef":       "string",
		"RefFrom":      "string",
		"RelRefFrom":   "string",
	}

	// Check for exact match
	if fieldType, exists := fieldTypeMap[fieldName]; exists {
		return fieldType
	}

	// Check for nested field access (e.g., Site.Title)
	parts := strings.Split(fieldName, ".")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		if fieldType, exists := fieldTypeMap[lastPart]; exists {
			return fieldType
		}
	}

	// Default to string for unknown fields
	return "string"
}

func (c *schemaCmd) getFieldDescription(fieldName string) string {
	descriptions := map[string]string{
		"Title":       "The title of the page or site",
		"Content":     "The main content of the page",
		"Summary":     "A summary or excerpt of the content",
		"Description": "A description of the page or site",
		"Date":        "The publication date",
		"Lastmod":     "The last modification date",
		"PublishDate": "The publish date",
		"ExpiryDate":  "The expiry date",
		"URL":         "The URL of the page",
		"Permalink":   "The permanent link to the page",
		"RelPermalink": "The relative permanent link to the page",
		"Type":        "The type of the page",
		"Kind":        "The kind of the page",
		"Section":     "The section of the page",
		"Layout":      "The layout template to use",
		"Lang":        "The language code",
		"LangDir":     "The language direction",
		"Dir":         "The directory path",
		"Weight":      "The weight for sorting",
		"WordCount":   "The number of words in the content",
		"ReadingTime": "The estimated reading time in minutes",
		"FuzzyWordCount": "The fuzzy word count",
		"Draft":       "Whether the page is a draft",
		"Publish":     "Whether the page should be published",
		"IsHome":      "Whether this is the home page",
		"IsPage":      "Whether this is a page",
		"IsSection":   "Whether this is a section",
		"IsNode":      "Whether this is a node",
		"IsMenuCurrent": "Whether this menu item is current",
		"HasMenuCurrent": "Whether this menu has a current item",
		"Params":      "Custom parameters from front matter",
		"Resources":   "Page resources (images, etc.)",
		"Pages":       "Child pages",
		"RegularPages": "Regular child pages",
		"AllTranslations": "All translations of this page",
		"Translations": "Translations of this page",
		"AlternativeOutputFormats": "Alternative output formats",
		"OutputFormats": "Available output formats",
		"File":         "File information",
		"GitInfo":      "Git information",
		"Site":         "Site-wide information",
		"Taxonomies":   "Taxonomy information",
		"Data":         "Data files",
		"Menus":        "Menu information",
		"Home":         "Home page information",
		"Next":         "Next page",
		"Prev":         "Previous page",
		"NextInSection": "Next page in section",
		"PrevInSection": "Previous page in section",
		"Parent":       "Parent page",
		"CurrentSection": "Current section",
		"FirstSection": "First section",
		"InSection":    "Whether in section",
		"Ancestors":    "Ancestor pages",
		"Current":      "Current page",
		"CurrentPage":  "Current page",
		"Paginator":    "Pagination information",
		"Scratch":      "Scratch space for variables",
		"Ref":          "Reference to another page",
		"RelRef":       "Relative reference to another page",
		"RefFrom":      "Reference from another page",
		"RelRefFrom":   "Relative reference from another page",
	}

	// Check for exact match
	if desc, exists := descriptions[fieldName]; exists {
		return desc
	}

	// Check for nested field access
	parts := strings.Split(fieldName, ".")
	if len(parts) > 1 {
		lastPart := parts[len(parts)-1]
		if desc, exists := descriptions[lastPart]; exists {
			return desc
		}
	}

	return ""
}
