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
	"sort"
	"strings"

	"github.com/gohugoio/hugo/tpl/tplimpl"
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

    // Extract variables and generate schema using Hugo analyzer
    schema := c.generateSchemaFromTemplate(templatePath, string(content))

	// Output JSON schema
	jsonOutput, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	fmt.Println(string(jsonOutput))
	return nil
}

func (c *schemaCmd) generateSchemaFromTemplate(templatePath string, content string) *TemplateSchema {
	schema := &TemplateSchema{
		Title:       fmt.Sprintf("Schema for %s", filepath.Base(templatePath)),
		Description: fmt.Sprintf("JSON schema for Hugo template: %s", templatePath),
		Type:        "object",
		Properties:  make(map[string]interface{}),
		Required:    []string{},
		Variables:   []TemplateVariable{},
	}

    // Analyze using Hugo's parser/AST via tplimpl
    analysis, err := tplimpl.AnalyzeTemplate(filepath.Base(templatePath), content, false)
    if err != nil {
        // Fall back to empty schema on parse error
        return schema
    }

	// Convert variables to schema properties
    for _, v := range analysis.Vars {
        tv := TemplateVariable{
            Name:        v.Name,
            Type:        func() string { if v.IsArray { return "array" }; return c.inferType(v.Name, "") }(),
            Description: c.getFieldDescription(v.Name),
            Required:    v.Required,
        }
        schema.Variables = append(schema.Variables, tv)
        schema.Properties[v.Name] = map[string]interface{}{
            "type":        tv.Type,
            "description": tv.Description,
        }
        if v.Required {
            schema.Required = append(schema.Required, v.Name)
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

// regex-based helpers removed in favor of native Hugo AST analyzer

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
