package model

import (
	"sort"

	"github.com/99designs/gqlgen/codegen/config"
	"github.com/99designs/gqlgen/codegen/templates"
	"github.com/vektah/gqlparser/ast"
)

type args struct {
	PackageName string
	Models      []model
	Enums       []*ast.Definition
}

type model struct {
	*ast.Definition
	Implements []string
	Fields     []field
}

type field struct {
	*ast.FieldDefinition

	GoName string
}

func Generate(cfg *config.Config, s *ast.Schema) (config.TypeMap, error) {
	r, err := templates.NewRenderer(cfg, s)
	if err != nil {
		return nil, err
	}

	tplArgs := args{
		PackageName: cfg.Model.Package,
	}

	for _, typ := range r.Schema.Types {
		if typ == r.Schema.Query || typ == r.Schema.Mutation || typ == r.Schema.Subscription || cfg.Models.IsDefined(typ.Name) {
			continue
		}

		if typ.Kind == ast.Object || typ.Kind == ast.InputObject || typ.Kind == ast.Interface || typ.Kind == ast.Union {

			mod := model{
				Definition: typ,
			}
			for _, f := range typ.Fields {
				goName := f.Name
				if mod, found := cfg.Models[typ.Name]; found && mod.Fields[f.Name].FieldName != "" {
					goName = mod.Fields[f.Name].FieldName
				}
				mod.Fields = append(mod.Fields, field{
					GoName:          goName,
					FieldDefinition: f,
				})
			}
			for _, intf := range r.Schema.GetImplements(typ) {
				mod.Implements = append(mod.Implements, intf.Name)
			}
			tplArgs.Models = append(tplArgs.Models, mod)
		}

		if typ.Kind == ast.Enum {
			tplArgs.Enums = append(tplArgs.Enums, typ)
		}
	}

	sort.Slice(tplArgs.Models, func(i, j int) bool {
		return tplArgs.Models[i].Name < tplArgs.Models[j].Name
	})

	sort.Slice(tplArgs.Enums, func(i, j int) bool {
		return tplArgs.Enums[i].Name < tplArgs.Enums[j].Name
	})

	if err = r.RenderToFile("model/models.gotpl", cfg.Model.Filename, tplArgs); err != nil {
		return nil, err
	}

	generated := config.TypeMap{}

	for _, model := range tplArgs.Models {
		generated[model.Name] = config.TypeMapEntry{
			Model: cfg.Model.ImportPath() + "." + templates.PubTypeLit(model.Name),
		}
	}

	for _, enum := range tplArgs.Enums {
		generated[enum.Name] = config.TypeMapEntry{
			Model: cfg.Model.ImportPath() + "." + templates.PubTypeLit(enum.Name),
		}
	}

	return generated, nil
}
