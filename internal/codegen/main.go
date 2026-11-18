// Command codegen generates the portone package's types and clients from PortOne's OpenAPI document,
// the same one upstream's server SDKs are generated from
// (https://github.com/portone-io/server-sdk/blob/main/codegen/openapi.json).
//
// Run it from the module root with `go generate ./...`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	specPath := flag.String("spec", "internal/codegen/openapi.json", "OpenAPI document")
	outDir := flag.String("out", ".", "package directory to write *.gen.go files into")
	flag.Parse()

	raw, err := os.ReadFile(*specPath)
	if err != nil {
		return err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse spec: %w", err)
	}
	m, err := buildModel(&spec)
	if err != nil {
		return fmt.Errorf("build model: %w", err)
	}

	g := newGen(m)
	if err := generate(g); err != nil {
		return err
	}

	old, err := filepath.Glob(filepath.Join(*outDir, "*.gen.go"))
	if err != nil {
		return err
	}
	oldTests, err := filepath.Glob(filepath.Join(*outDir, "*.gen_test.go"))
	if err != nil {
		return err
	}
	old = append(old, oldTests...)
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(g.files))
	for name := range g.files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		src, err := render(name, m.version, g.files[name].Bytes())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(*outDir, name), src, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("generated %d files from PortOne OpenAPI %s (%d operations)\n", len(names), m.version, len(m.ops))
	return nil
}

// generate turns the generator's panics on unsupported spec shapes into an error.
func generate(g *gen) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("generate: %v", r)
		}
	}()
	g.run()
	return nil
}
