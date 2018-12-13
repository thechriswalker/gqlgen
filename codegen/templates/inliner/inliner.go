package main

import (
	"bytes"
	"go/format"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dir := "./"

	out := bytes.Buffer{}
	out.WriteString("package templates\n\n")
	out.WriteString("var data = map[string]string{\n")

	walkErr := filepath.Walk(dir, func(path string, f os.FileInfo, err error) error {
		if !strings.HasSuffix(f.Name(), ".gotpl") {
			return nil
		}

		b, err := ioutil.ReadFile(path)
		if err != nil {
			panic(err)
		}

		out.WriteString(strconv.Quote(path))
		out.WriteRune(':')
		out.WriteString(strconv.Quote(string(b)))
		out.WriteString(",\n")

		return nil
	})
	if walkErr != nil {
		panic(walkErr)
	}

	out.WriteString("}\n")

	formatted, err2 := format.Source(out.Bytes())
	if err2 != nil {
		panic(err2)
	}

	ioutil.WriteFile(dir+"data.go", formatted, 0644)
}
