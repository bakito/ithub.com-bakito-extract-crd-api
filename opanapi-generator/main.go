package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/bakito/extract-crd-api/internal/flags"
	apiv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Helper function to convert string to CamelCase.
func toCamelCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(string(word[0])) + strings.ToLower(word[1:])
		}
	}

	return strings.Join(words, "")
}

// Helper function to map OpenAPI types to Go types.
func mapType(prop apiv1.JSONSchemaProps) string {
	if prop.Type == "" {
		if prop.Ref != nil {
			parts := strings.Split(*prop.Ref, "/")
			return toCamelCase(parts[len(parts)-1])
		}
		return "any"
	}

	switch prop.Type {
	case "string":
		if prop.Format != "" {
			switch prop.Format {
			case "date-time":
				return "metav1.Time"
			case "byte", "binary":
				return "[]byte"
			}
		}
		return "string"
	case "integer", "number":
		if prop.Format != "" {
			switch prop.Format {
			case "int32":
				return "int32"
			case "int64":
				return "int64"
			case "float":
				return "float32"
			case "double":
				return "float64"
			}
		}
		if prop.Type == "integer" {
			return "int64"
		}
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		if prop.Items != nil && prop.Items.Schema != nil {
			itemType := mapType(*prop.Items.Schema)
			return "[]" + itemType
		}
		return "[]any"
	case "object":
		// We don't need to mark this for later replacement since we'll handle object types
		// directly in the generateStructs function
		return "map[string]any"
	default:
		return "any"
	}
}

// Extract schemas from CRD.
func extractSchemas(crd apiv1.CustomResourceDefinition, desiredVersion string) (*apiv1.JSONSchemaProps, string) {
	// Try to get schema from new CRD format first (v1)
	if len(crd.Spec.Versions) > 0 {
		for _, v := range crd.Spec.Versions {
			if v.Storage && (desiredVersion == "" || desiredVersion == v.Name) {
				return v.Schema.OpenAPIV3Schema, v.Name
			}
		}
	}

	return nil, ""
}

// Process schema and generate enums.
func generateEnum(prop *apiv1.JSONSchemaProps, fieldName string) (enums []EnumDef) {
	for _, e := range prop.Enum {
		val := string(e.Raw)
		enums = append(enums, EnumDef{
			Name:  fieldName + toCamelCase(strings.ReplaceAll(val, `"`, "")),
			Value: val,
		})
	}
	return enums
}

// Process schema and generate structs.
func generateStructs(
	schema *apiv1.JSONSchemaProps,
	name string,
	structMap map[string]*StructDef,
	path string,
	root bool,
	imports map[string]bool,
) {
	structDef := &StructDef{
		Root:        root,
		Name:        name,
		Description: fmt.Sprintf("%s represents a %s", name, path),
	}
	structMap[name] = structDef

	for propName, prop := range schema.Properties {
		fieldName := toCamelCase(propName)
		var fieldType string

		if prop.Type != "" { //nolint:gocritic
			fieldType = mapType(prop)

			// Handle nested objects by creating a new struct
			switch prop.Type {
			case "object":
				if len(prop.Properties) > 0 {
					nestedName := name + fieldName
					fieldType = nestedName
					generateStructs(&prop, nestedName, structMap, path+"."+propName, false, imports)
				} else {
					if prop.AdditionalProperties != nil && prop.AdditionalProperties.Schema != nil {
						fieldType = "map[string]" + mapType(*prop.AdditionalProperties.Schema)
					} else {
						// Object with no properties, use map
						fieldType = "map[string]any"
					}
				}
			case "array":
				if prop.Items != nil && prop.Items.Schema != nil && prop.Items.Schema.Type == "object" {
					nestedName := name + fieldName
					generateStructs(prop.Items.Schema, nestedName, structMap, path+"."+propName, false, imports)
					fieldType = "[]" + nestedName
				}
			default:
				fieldType = mapType(prop)
			}
		} else if prop.Ref != nil {
			// Handle references
			parts := strings.Split(*prop.Ref, "/")
			fieldType = toCamelCase(parts[len(parts)-1])
		} else {
			fieldType = "*apiextensionsv1.JSON"
			imports[`apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"`] = true
		}

		field := FieldDef{
			Name:        fieldName,
			JSONTag:     propName,
			Description: prop.Description,
		}

		if prop.Items != nil && len(prop.Items.Schema.Enum) > 0 {
			nestedName := name + fieldName
			field.Enums = generateEnum(prop.Items.Schema, nestedName)
			field.EnumType = prop.Items.Schema.Type
			fieldType = "[]" + nestedName
			field.EnumName = nestedName
		} else if len(prop.Enum) > 0 {
			nestedName := name + fieldName
			field.Enums = generateEnum(&prop, nestedName)
			field.EnumType = prop.Type
			field.EnumName = nestedName
			fieldType = nestedName
		}

		field.Type = fieldType

		structDef.Fields = append(structDef.Fields, field)
	}
}

func prepareDescription(desc string, field bool) string {
	indent := "// "
	if field {
		indent = "\t" + indent
	}
	return strings.ReplaceAll(desc, "\n", "\n"+indent)
}

var (
	crds    flags.ArrayFlags
	target  string
	version string
)

func main() {
	flag.Var(&crds, "crd", "CRD file to process")
	flag.StringVar(&target, "target", "", "The target directory to copyFile the files to")
	flag.StringVar(&version, "version", "", "The version to select from the CRD; If not defined, the first version is used")
	flag.Parse()

	if strings.TrimSpace(target) == "" {
		slog.Error("Flag must be defined", "flag", "target")
		return
	}
	if len(crds) == 0 {
		slog.Error("At lease on CRD must be defined", "flag", "target")
		return
	}

	var crdGroup string
	var crdKind string
	var names []CRDNames

	var files []outFile
	for i, crd := range crds {
		// Read first crd file
		data, err := os.ReadFile(crd)
		if err != nil {
			slog.Error("Error reading file", "error", err)
			return
		}

		// Parse CRD YAML
		var crd apiv1.CustomResourceDefinition
		err = yaml.Unmarshal(data, &crd)
		if err != nil {
			slog.Error("Error parsing YAML", "error", err)
			return
		}
		// Extract CRD info

		if i > 0 && crdGroup != crd.Spec.Group {
			slog.Error(
				"Not all CRD have the same group",
				"group-a", crdGroup, "kind-a", crdKind,
				"group-b", crd.Spec.Group, "kind-b", crd.Spec.Names.Kind,
			)
			return
		}
		crdKind = crd.Spec.Names.Kind
		crdPlural := crd.Spec.Names.Plural
		crdList := crd.Spec.Names.ListKind
		names = append(names, CRDNames{Kind: crdKind, List: crdList})
		crdGroup = crd.Spec.Group

		// Extract schema
		schema, foundVersion := extractSchemas(crd, version)
		if schema == nil {
			slog.Error("Could not find OpenAPI schema in CRD", "version", version)
			return
		}

		if version != "" && version != foundVersion {
			slog.Error(
				"Not all CRD have the same version",
				"group-a", crdGroup, "version-a", version, "kind-a", crdKind,
				"group-b", crd.Spec.Group, "version-b", foundVersion, "kind-b", crd.Spec.Names.Kind,
			)
			return
		}
		version = foundVersion

		// Generate structs
		structMap := make(map[string]*StructDef)
		imports := map[string]bool{`metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"`: true}
		generateStructs(schema, crdKind, structMap, crdKind, true, imports)

		// Generate types code
		typesCode := generateTypesCode(structMap, version, crdKind, crdPlural, crdGroup, crdList, imports)

		// Write output file
		outputFile := filepath.Join(target, version, fmt.Sprintf("types_%s.go", strings.ToLower(crdKind)))
		files = append(files, outFile{
			name:       outputFile,
			content:    typesCode,
			successMsg: "Successfully generated Go structs",
			successArgs: []any{
				"group", crdGroup,
				"version", version,
				"kind", crdKind,
				"file", outputFile,
			},
		})
	}

	// Generate GroupVersionInfo code
	gvi, err := generateGroupVersionInfoCode(crdGroup, version, names)
	if err != nil {
		slog.Error("Error writing group_version_kind.go", "error", err)
		return
	}

	// Write output file
	outputFile := filepath.Join(target, version, "group_version_info.go")

	files = append(files, outFile{
		name:       outputFile,
		content:    gvi,
		successMsg: "Successfully generated GroupVersionInfo",
		successArgs: []any{
			"group", crdGroup, "version", version, "file", outputFile,
		},
	})

	writeFiles(files)
}

func writeFiles(files []outFile) {
	for _, f := range files {
		dir := filepath.Dir(f.name)

		// Create the directory if it doesn't exist
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			slog.Error("Error creating directory", "error", err)
			return
		}

		if err := os.WriteFile(f.name, []byte(f.content), 0o644); err != nil {
			slog.Error("Error writing output file", "error", err)
			return
		}

		slog.With(f.successArgs...).Info(f.successMsg)
	}
}

type outFile struct {
	name        string
	content     string
	successMsg  string
	successArgs []any
}
