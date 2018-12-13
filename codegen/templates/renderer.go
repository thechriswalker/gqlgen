package templates

import (
	"bytes"
	"fmt"
	"go/build"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/99designs/gqlgen/internal/gopath"

	"unicode"

	"github.com/99designs/gqlgen/codegen/config"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/ast"
	"golang.org/x/tools/go/loader"
)

type Renderer struct {
	CurrentImports *Imports
	Config         *config.Config
	Schema         *ast.Schema
	Prog           *loader.Program
}

func NewRenderer(cfg *config.Config, s *ast.Schema) (*Renderer, error) {
	ldr := cfg.NewLoaderWithoutErrors()
	p, err := ldr.Load()
	if err != nil {
		return nil, err
	}

	return &Renderer{
		Schema: s,
		Config: cfg,
		Prog:   p,
	}, nil
}

func (r *Renderer) Run(name string, tpldata interface{}) (*bytes.Buffer, error) {
	t := template.New("").Funcs(template.FuncMap{
		"ucFirst":       ucFirst,
		"lcFirst":       lcFirst,
		"quote":         strconv.Quote,
		"rawQuote":      rawQuote,
		"pubTypeLit":    PubTypeLit,
		"privTypeLit":   PrivTypeLit,
		"pubId":         PubIdentifier,
		"toCamel":       ToCamel,
		"privId":        PrivIdentifier,
		"ref":           r.Ref,
		"dump":          dump,
		"prefixLines":   prefixLines,
		"reserveImport": r.reserveImport,
		"lookupImport":  r.lookupImport,
	})

	for filename, data := range data {
		if strings.HasPrefix(filename, "partials") {
			_, err := t.New(filename).Parse(data)
			if err != nil {
				panic(err)
			}
		}
	}

	tpl, err := t.New(name).Parse(data[name])
	if err != nil {
		panic(err)
	}

	buf := &bytes.Buffer{}
	err = tpl.Execute(buf, tpldata)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func (r *Renderer) reserveImport(path string, aliases ...string) string {
	return r.CurrentImports.Reserve(path, aliases...)
}

func (r *Renderer) lookupImport(path string) string {
	return r.CurrentImports.Lookup(path)
}

func (r *Renderer) RenderToFile(tpl string, filename string, data interface{}) error {
	if r.CurrentImports != nil {
		panic(fmt.Errorf("recursive or concurrent call to RenderToFile detected"))
	}
	r.CurrentImports = &Imports{destDir: filepath.Dir(filename)}

	var buf *bytes.Buffer
	buf, err := r.Run(tpl, data)
	if err != nil {
		return errors.Wrap(err, filename+" generation failed")
	}

	b := bytes.Replace(buf.Bytes(), []byte("%%%IMPORTS%%%"), []byte(r.CurrentImports.String()), -1)
	r.CurrentImports = nil

	return write(filename, b)
}

// TypeLit turns a string into a well formed, valid, go TypeLit https://golang.org/ref/spec#TypeLit
// for example in
//   type foo struct {}
// foo is a TypeLit
func TypeLit(s string) string {
	return LintName(ToCamel(s))
}

// PubTypeLit is a TypeLit with the first character uppercase, so it is publically exported
func PubTypeLit(s string) string {
	return ucFirst(TypeLit(s))
}

// PrivTypeLit is a TypeLit with the first character lowercase, so it is not exported
func PrivTypeLit(s string) string {
	return lcFirst(TypeLit(s))
}

// Identifier turns a string into a well formed, valid, go udentifier https://golang.org/ref/spec#identifier
// for example in
//   type foo struct {
//     Foo string
//   }
// Foo is an identifier
//
// and in
//   var foo i
// foo is also an identifier
func Identifier(s string) string {
	return LintName(ToCamel(s))
}

// PubIdentifier is an Identifer with the first character uppercase, so it is publically exported
func PubIdentifier(s string) string {
	return ucFirst(Identifier(s))
}

// PrivIdentifier is an Identifer with the first character lowercase, so it is not exported
func PrivIdentifier(s string) string {
	return lcFirst(Identifier(s))
}

func (r *Renderer) Ref(in *ast.Type) string {
	mods := r.typeModifiersGQL(in)
	if r.Schema.Types[in.Name()].Kind == ast.Interface {
		mods = mods.PopPtr()
	}

	if !r.Config.Models.IsDefined(in.Name()) {
		// If its not in the typemap, assume model generation is in the process of building it
		// TODO: Make configurable

		return mods.String() + r.refRaw(r.Config.Model.Package, PubTypeLit(in.Name()))
	}

	pkg, typ := gopath.PkgAndType(r.Config.Models[in.Name()].Model)

	var boundType types.Type

	def, err := r.FindGoType(pkg, "Unmarshal"+typ)
	if err == nil {
		switch def := def.(type) {
		case *types.Func:
			sig := def.Type().(*types.Signature)
			boundType = sig.Results().At(0).Type()
		}
	} else {
		o, err := r.FindGoType(pkg, typ)
		if err != nil {
			panic(err)
		}
		boundType = o.Type()
	}

	return mods.String() + types.TypeString(boundType, r.qualifier)
}

func (r *Renderer) qualifier(i *types.Package) string {
	return r.CurrentImports.Lookup(i.Path())
}

func (r *Renderer) refRaw(pkg string, typ string) string {
	imp := r.CurrentImports.Lookup(r.Config.Model.ImportPath())
	if imp == "" {
		return typ
	}
	return imp + "." + typ
}

type mods []string

func (m mods) String() string {
	return strings.Join(m, "")
}

func (m mods) PopPtr() mods {
	if len(m) == 0 || m[len(m)-1] != "*" {
		return nil
	}

	return m[0 : len(m)-1]
}

func (r *Renderer) typeModifiersGQL(t *ast.Type) mods {
	var modifiers []string
	for {
		if t.Elem != nil {
			modifiers = append(modifiers, "[]")
			t = t.Elem
		} else {
			if !t.NonNull {
				modifiers = append(modifiers, "*")
			}
			return modifiers
		}
	}
}

func (r *Renderer) FindGoType(pkgName string, typeName string) (types.Object, error) {
	if pkgName == "" {
		return nil, nil
	}
	fullName := typeName
	if pkgName != "" {
		fullName = pkgName + "." + typeName
	}

	pkgName, err := r.ResolvePkg(pkgName)
	if err != nil {
		return nil, errors.Errorf("unable to resolve package for %s: %s\n", fullName, err.Error())
	}

	pkg := r.Prog.Imported[pkgName]
	if pkg == nil {
		return nil, errors.Errorf("required package was not loaded: %s", fullName)
	}

	for astNode, def := range pkg.Defs {
		if astNode.Name != typeName || def.Parent() == nil || def.Parent() != pkg.Pkg.Scope() {
			continue
		}

		return def, nil
	}

	return nil, errors.Errorf("unable to find type %s\n", fullName)
}

func (r *Renderer) ResolvePkg(pkgName string) (string, error) {
	cwd, _ := os.Getwd()

	pkg, err := build.Default.Import(pkgName, cwd, build.FindOnly)
	if err != nil {
		return "", err
	}

	return pkg.ImportPath, nil
}

// copy from https://github.com/golang/lint/blob/06c8688daad7faa9da5a0c2f163a3d14aac986ca/lint.go#L679

// lintName returns a different name if it should be different.
func LintName(name string) (should string) {
	// Fast path for simple cases: "_" and all lowercase.
	if name == "_" {
		return name
	}
	allLower := true
	for _, r := range name {
		if !unicode.IsLower(r) {
			allLower = false
			break
		}
	}
	if allLower {
		return name
	}

	// Split camelCase at any lower->upper transition, and split on underscores.
	// Check each word for common initialisms.
	runes := []rune(name)
	w, i := 0, 0 // index of start of word, scan
	for i+1 <= len(runes) {
		eow := false // whether we hit the end of a word
		if i+1 == len(runes) {
			eow = true
		} else if runes[i+1] == '_' {
			// underscore; shift the remainder forward over any run of underscores
			eow = true
			n := 1
			for i+n+1 < len(runes) && runes[i+n+1] == '_' {
				n++
			}

			// Leave at most one underscore if the underscore is between two digits
			if i+n+1 < len(runes) && unicode.IsDigit(runes[i]) && unicode.IsDigit(runes[i+n+1]) {
				n--
			}

			copy(runes[i+1:], runes[i+n+1:])
			runes = runes[:len(runes)-n]
		} else if unicode.IsLower(runes[i]) && !unicode.IsLower(runes[i+1]) {
			// lower->non-lower
			eow = true
		}
		i++
		if !eow {
			continue
		}

		// [w,i) is a word.
		word := string(runes[w:i])
		if u := strings.ToUpper(word); commonInitialisms[u] {
			// Keep consistent case, which is lowercase only at the start.
			if w == 0 && unicode.IsLower(runes[w]) {
				u = strings.ToLower(u)
			}
			// All the common initialisms are ASCII,
			// so we can replace the bytes exactly.
			copy(runes[w:], []rune(u))
		} else if w > 0 && strings.ToLower(word) == word {
			// already all lowercase, and not the first word, so uppercase the first character.
			runes[w] = unicode.ToUpper(runes[w])
		}
		w = i
	}
	return string(runes)
}

// commonInitialisms is a set of common initialisms.
// Only add entries that are highly unlikely to be non-initialisms.
// For instance, "ID" is fine (Freudian code is rare), but "AND" is not.
var commonInitialisms = map[string]bool{
	"ACL":   true,
	"API":   true,
	"ASCII": true,
	"CPU":   true,
	"CSS":   true,
	"DNS":   true,
	"EOF":   true,
	"GUID":  true,
	"HTML":  true,
	"HTTP":  true,
	"HTTPS": true,
	"ID":    true,
	"IP":    true,
	"JSON":  true,
	"LHS":   true,
	"QPS":   true,
	"RAM":   true,
	"RHS":   true,
	"RPC":   true,
	"SLA":   true,
	"SMTP":  true,
	"SQL":   true,
	"SSH":   true,
	"TCP":   true,
	"TLS":   true,
	"TTL":   true,
	"UDP":   true,
	"UI":    true,
	"UID":   true,
	"UUID":  true,
	"URI":   true,
	"URL":   true,
	"UTF8":  true,
	"VM":    true,
	"XML":   true,
	"XMPP":  true,
	"XSRF":  true,
	"XSS":   true,
}
