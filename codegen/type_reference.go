package codegen

import (
	"strconv"
	"strings"

	"github.com/vektah/gqlparser/ast"
)

// TypeReference represents the type of a field or arg declaration
type TypeReference struct {
	Definition *TypeDefinition

	Modifiers   []string
	ASTType     *ast.Type
	AliasedType *TypeImplementation
}

func (t TypeReference) Signature() string {
	if t.AliasedType != nil {
		return strings.Join(t.Modifiers, "") + t.AliasedType.FullName()
	}
	return strings.Join(t.Modifiers, "") + t.Definition.FullName()
}

func (t TypeReference) FullSignature() string {
	pkg := ""
	if t.Definition.Package != "" {
		pkg = t.Definition.Package + "."
	}

	return strings.Join(t.Modifiers, "") + pkg + t.Definition.GoType
}

func (t TypeReference) IsPtr() bool {
	return len(t.Modifiers) > 0 && t.Modifiers[0] == modPtr
}

func (t *TypeReference) StripPtr() {
	if !t.IsPtr() {
		return
	}
	t.Modifiers = t.Modifiers[0 : len(t.Modifiers)-1]
}

func (t TypeReference) IsSlice() bool {
	return len(t.Modifiers) > 0 && t.Modifiers[0] == modList ||
		len(t.Modifiers) > 1 && t.Modifiers[0] == modPtr && t.Modifiers[1] == modList
}

func (t TypeReference) Unmarshal(result, raw string) string {
	return t.unmarshal(result, raw, t.Modifiers, 1)
}

func (t TypeReference) unmarshal(result, raw string, remainingMods []string, depth int) string {
	switch {
	case len(remainingMods) > 0 && remainingMods[0] == modPtr:
		ptr := "ptr" + strconv.Itoa(depth)
		return tpl(`var {{.ptr}} {{.mods}}{{.t.Definition.FullName}}
			if {{.raw}} != nil {
				{{.next}}
				{{.result}} = &{{.ptr -}}
			}
		`, map[string]interface{}{
			"ptr":    ptr,
			"t":      t,
			"raw":    raw,
			"result": result,
			"mods":   strings.Join(remainingMods[1:], ""),
			"next":   t.unmarshal(ptr, raw, remainingMods[1:], depth+1),
		})

	case len(remainingMods) > 0 && remainingMods[0] == modList:
		var rawIf = "rawIf" + strconv.Itoa(depth)
		var index = "idx" + strconv.Itoa(depth)

		return tpl(`var {{.rawSlice}} []interface{}
			if {{.raw}} != nil {
				if tmp1, ok := {{.raw}}.([]interface{}); ok {
					{{.rawSlice}} = tmp1
				} else {
					{{.rawSlice}} = []interface{}{ {{.raw}} }
				}
			}
			{{.result}} = make({{.type}}, len({{.rawSlice}}))
			for {{.index}} := range {{.rawSlice}} {
				{{ .next -}}
			}`, map[string]interface{}{
			"raw":      raw,
			"rawSlice": rawIf,
			"index":    index,
			"result":   result,
			"type":     strings.Join(remainingMods, "") + t.Definition.FullName(),
			"next":     t.unmarshal(result+"["+index+"]", rawIf+"["+index+"]", remainingMods[1:], depth+1),
		})
	}

	realResult := result
	if t.AliasedType != nil {
		result = "castTmp"
	}

	return tpl(`{{- if .t.AliasedType }}
			var castTmp {{.t.Definition.FullName}}
		{{ end }}
			{{- if eq .t.Definition.GoType "map[string]interface{}" }}
				{{- .result }} = {{.raw}}.(map[string]interface{})
			{{- else if .t.Definition.Marshaler }}
				{{- .result }}, err = {{ .t.Definition.Marshaler.PkgDot }}Unmarshal{{.t.Definition.Marshaler.GoType}}({{.raw}})
			{{- else -}}
				err = (&{{.result}}).UnmarshalGQL({{.raw}})
			{{- end }}
		{{- if .t.AliasedType }}
			{{ .realResult }} = {{.t.AliasedType.FullName}}(castTmp)
		{{- end }}`, map[string]interface{}{
		"realResult": realResult,
		"result":     result,
		"raw":        raw,
		"t":          t,
	})
}

func (t TypeReference) Marshal(val string) string {
	if t.AliasedType != nil {
		val = t.Definition.GoType + "(" + val + ")"
	}

	if t.Definition.Marshaler != nil {
		return "return " + t.Definition.Marshaler.PkgDot() + "Marshal" + t.Definition.Marshaler.GoType + "(" + val + ")"
	}

	return "return " + val
}
