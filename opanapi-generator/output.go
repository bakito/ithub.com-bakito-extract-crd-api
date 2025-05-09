package main

import (
	_ "embed"
	"maps"
	"slices"
	"sort"
	"strings"
	"text/template"
)

const myName = "opanapi-generator"

var (
	//go:embed group_version_into.go.tpl
	gviTpl string
	//go:embed types.go.tpl
	typeTpl string
)

// Generate Go code from struct definitions.
func generateTypesCode(
	structMap map[string]*StructDef,
	version, crdKind, plural, crdGroup, crdList string,
	imports map[string]bool,
) string {
	// Sort and generate structs
	sortedStructNames := slices.Sorted(maps.Keys(structMap))

	var root *StructDef
	var structs []*StructDef

	importList := slices.Sorted(maps.Keys(imports))

	for _, structName := range sortedStructNames {
		structDef := structMap[structName]

		structDef.Description = prepareDescription(structDef.Description, false)

		sort.Slice(structDef.Fields, func(i, j int) bool {
			return structDef.Fields[i].Name < structDef.Fields[j].Name
		})

		for i, f := range structDef.Fields {
			structDef.Fields[i].Description = prepareDescription(f.Description, true)
		}

		if structName == crdKind {
			// keep only spec and status
			var rootFields []FieldDef
			for _, field := range structDef.Fields {
				if field.JSONTag == "spec" || field.JSONTag == "status" {
					rootFields = append(rootFields, field)
				}
			}
			root = structDef
			root.Fields = rootFields
		} else {
			structs = append(structs, structDef)
		}
	}

	var sb strings.Builder
	t := template.Must(template.New("types.go.tpl").Parse(typeTpl))
	if err := t.Execute(&sb, map[string]any{
		"AppName": myName,
		"Version": version,
		"Group":   crdGroup,
		"Kind":    crdKind,
		"List":    crdList,
		"Plural":  toCamelCase(plural),
		"Root":    root,
		"Structs": structs,
		"Imports": importList,
	}); err != nil {
		println(err)
	}
	return sb.String()
}

func generateGroupVersionInfoCode(group, version string, names []CRDNames) (string, error) {
	var sb strings.Builder
	t := template.Must(template.New("group_version_into.go.tpl").Parse(gviTpl))
	if err := t.Execute(&sb, map[string]any{
		"AppName":  myName,
		"Version":  version,
		"Group":    group,
		"CRDNames": names,
	}); err != nil {
		return "", err
	}

	return sb.String(), nil
}
