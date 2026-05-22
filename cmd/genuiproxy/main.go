// Command genuiproxy generates the UI-side proxy for the collector/UI split
// (ADR 0001, Phase 2). It loads internal/app, and for every exported *App
// method that is a frontend RPC (i.e. not one of the lifecycle/tray/hotkey
// special-cases from Anhang A) it emits a proxy method with the identical
// signature that forwards the call to the collector via the generic Invoke
// RPC. Keeping the signatures identical means the Wails bindings — and hence
// the frontend api layer — are unchanged.
//
// Run via `go generate ./internal/uiproxy`. The generated proxy_gen.go is
// checked in so the build needs no codegen toolchain.
package main

import (
	"fmt"
	"go/format"
	"go/types"
	"log"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	appPkgPath = "github.com/dusthoff/hashpoint/internal/app"
	outFile    = "internal/uiproxy/proxy_gen.go"
)

// special lists the exported *App methods that are NOT frontend RPCs: Wails
// lifecycle hooks, tray/hotkey entry points and window-local actions handled
// by the UI itself (ADR Anhang A, "Sonderbehandlung"). They are not proxied.
var special = map[string]bool{
	"Startup":             true,
	"Shutdown":            true,
	"OnWindowBeforeClose": true,
	"Quit":                true,
	"ShowWindow":          true,
	"OpenHelpTab":         true,
	"RequestSyncToday":    true,
	"FireQuickTag":        true,
	"QuickTagOpen":        true,
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("genuiproxy: %v", err)
	}
}

func run() error {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, appPkgPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", appPkgPath, err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		return fmt.Errorf("package %s has errors", appPkgPath)
	}
	appPkg := pkgs[0]

	obj := appPkg.Types.Scope().Lookup("App")
	if obj == nil {
		return fmt.Errorf("type App not found in %s", appPkgPath)
	}
	named, ok := obj.Type().(*types.Named)
	if !ok {
		return fmt.Errorf("type App is not a *types.Named")
	}

	errIface := types.Universe.Lookup("error").Type()
	imports := map[string]string{} // import path → package name
	qual := func(p *types.Package) string {
		if p == nil || p == appPkg.Types {
			imports[appPkgPath] = "app"
			return "app"
		}
		imports[p.Path()] = p.Name()
		return p.Name()
	}

	mset := types.NewMethodSet(types.NewPointer(named))
	var methods []string
	for i := 0; i < mset.Len(); i++ {
		fn, ok := mset.At(i).Obj().(*types.Func)
		if !ok || !fn.Exported() || special[fn.Name()] {
			continue
		}
		sig, ok := fn.Type().(*types.Signature)
		if !ok {
			continue
		}
		gen, ok := genMethod(fn.Name(), sig, errIface, qual)
		if !ok {
			log.Printf("skip %s: unsupported signature", fn.Name())
			continue
		}
		methods = append(methods, gen)
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i] < methods[j] })

	src := assemble(imports, methods)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format generated source: %w\n%s", err, src)
	}
	if err := os.WriteFile(outFile, formatted, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", outFile, err)
	}
	log.Printf("genuiproxy: wrote %d proxy methods to %s", len(methods), outFile)
	return nil
}

// genMethod renders one proxy method, or ok=false when the signature shape is
// not supported (variadic, or an error result that is not the last return).
func genMethod(name string, sig *types.Signature, errIface types.Type, qual types.Qualifier) (string, bool) {
	if sig.Variadic() {
		return "", false
	}
	params := sig.Params()
	var paramDecls, argNames []string
	for i := 0; i < params.Len(); i++ {
		pn := fmt.Sprintf("p%d", i)
		paramDecls = append(paramDecls, pn+" "+types.TypeString(params.At(i).Type(), qual))
		argNames = append(argNames, pn)
	}
	args := "nil"
	if len(argNames) > 0 {
		args = "[]any{" + strings.Join(argNames, ", ") + "}"
	}

	// Partition results into data values and an optional trailing error.
	results := sig.Results()
	var dataTypes []string
	hasErr := false
	for i := 0; i < results.Len(); i++ {
		rt := results.At(i).Type()
		if types.Identical(rt, errIface) {
			if i != results.Len()-1 {
				return "", false // error must be the last result
			}
			hasErr = true
			continue
		}
		dataTypes = append(dataTypes, types.TypeString(rt, qual))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func (a *App) %s(%s) ", name, strings.Join(paramDecls, ", "))

	switch {
	case len(dataTypes) == 0 && !hasErr:
		fmt.Fprintf(&b, "{\n\t_ = a.call(%q, %s, nil)\n}", name, args)
	case len(dataTypes) == 0 && hasErr:
		fmt.Fprintf(&b, "error {\n\treturn a.call(%q, %s, nil)\n}", name, args)
	case len(dataTypes) == 1 && !hasErr:
		fmt.Fprintf(&b, "%s {\n\tvar r0 %s\n\t_ = a.call(%q, %s, &r0)\n\treturn r0\n}",
			dataTypes[0], dataTypes[0], name, args)
	case len(dataTypes) == 1 && hasErr:
		fmt.Fprintf(&b, "(%s, error) {\n\tvar r0 %s\n\terr := a.call(%q, %s, &r0)\n\treturn r0, err\n}",
			dataTypes[0], dataTypes[0], name, args)
	default: // two or more data values
		var decls, ptrs, rets []string
		for i, ts := range dataTypes {
			decls = append(decls, fmt.Sprintf("var r%d %s", i, ts))
			ptrs = append(ptrs, fmt.Sprintf("&r%d", i))
			rets = append(rets, fmt.Sprintf("r%d", i))
		}
		body := strings.Join(decls, "\n\t")
		if hasErr {
			fmt.Fprintf(&b, "(%s, error) {\n\t%s\n\terr := a.callMulti(%q, %s, []any{%s})\n\treturn %s, err\n}",
				strings.Join(dataTypes, ", "), body, name, args, strings.Join(ptrs, ", "), strings.Join(rets, ", "))
		} else {
			fmt.Fprintf(&b, "(%s) {\n\t%s\n\t_ = a.callMulti(%q, %s, []any{%s})\n\treturn %s\n}",
				strings.Join(dataTypes, ", "), body, name, args, strings.Join(ptrs, ", "), strings.Join(rets, ", "))
		}
	}
	return b.String(), true
}

func assemble(imports map[string]string, methods []string) string {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/genuiproxy. DO NOT EDIT.\n\n")
	b.WriteString("package uiproxy\n\n")

	paths := make([]string, 0, len(imports))
	for p := range imports {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	b.WriteString("import (\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "\t%q\n", p)
	}
	b.WriteString(")\n\n")

	for _, m := range methods {
		b.WriteString(m)
		b.WriteString("\n\n")
	}
	return b.String()
}
