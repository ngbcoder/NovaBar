// Command windump prints the contents of a .winmd metadata file.
//
// It exists to make the metadata inspectable without guessing. Every IID, vtable slot
// order and struct field list used by the media bridge is read from here, so a wrong
// value is visible in a diff rather than discovered as a crash.
//
// Usage:
//
//	windump <file.winmd> [namespace] [raw]
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ngbcoder/NovaBar/internal/winmd"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: windump <file.winmd> [namespace] [raw]")
		os.Exit(2)
	}

	f, err := winmd.Load(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ns := ""
	if len(os.Args) > 2 {
		ns = os.Args[2]
	}

	if ns == "" || ns == "-" {
		fmt.Println("namespaces:")
		for _, n := range f.Namespaces() {
			fmt.Println("  " + n)
		}
		return
	}

	defs := f.TypeDefsIn(ns)
	fmt.Printf("namespace %s: %d types\n", ns, len(defs))

	for _, def := range defs {
		kind := "class"
		if def.IsInterface() {
			kind = "interface"
		}
		fmt.Printf("\n%s %s (flags %#x)\n", kind, def.FullName(), def.Flags)
		if ext := f.ResolveTypeName(def.Extends); ext != "" {
			fmt.Printf("  extends %s\n", ext)
		}
		if g, ok := f.TypeGUID(def); ok {
			fmt.Printf("  guid %s\n", g)
		}

		for i, m := range f.Methods(def) {
			fmt.Printf("  slot %d: %s %s\n", i, m.Name, describe(f, m))
		}
		for _, fl := range f.Fields(def) {
			fmt.Printf("  field: %s %s\n", fl.Name, typeName(f, fl.Sig.Return))
		}
		for _, c := range f.Constants(def) {
			if v, ok := c.ConstantValue(); ok {
				fmt.Printf("  const: %s = %d\n", fieldName(f, def, c), v)
			}
		}
	}

	fmt.Println("\ninterface implementations:")
	for _, impl := range f.InterfaceImpls() {
		class := ""
		for _, def := range defs {
			if int(impl.Class) == def.Index {
				class = def.FullName()
			}
		}
		fmt.Printf("  %s implements %s\n", class, f.ResolveTypeName(impl.Interface))
	}

	fmt.Println("\nguid heap:")
	for i := uint32(1); i <= f.GUIDCount(); i++ {
		g, ok := f.GUIDAt(i)
		if !ok {
			break
		}
		fmt.Printf("  %3d %s\n", i, g)
	}
}

// fieldName finds the name of the field a constant belongs to.
func fieldName(f *winmd.File, def winmd.TypeDef, c winmd.Constant) string {
	for _, field := range f.Fields(def) {
		if field.Row == int(c.ParentIndex) {
			return field.Name
		}
	}
	return "?"
}

// describe renders a method's parameter types.
func describe(f *winmd.File, m winmd.Method) string {
	parts := make([]string, 0, len(m.Sig.Params))
	for _, p := range m.Sig.Params {
		parts = append(parts, typeName(f, p))
	}
	return "(" + strings.Join(parts, ", ") + ") -> " + typeName(f, m.Sig.Return)
}

// typeName renders a signature type, resolving type references to names.
func typeName(f *winmd.File, t winmd.Type) string {
	switch t.Kind {
	case winmd.KindClass, winmd.KindValueType:
		if name := f.ResolveTypeName(t.Ref); name != "" {
			return name
		}
		return t.Ref.String()
	case winmd.KindGenericInst:
		base := ""
		if t.Inner != nil {
			base = typeName(f, *t.Inner)
		}
		args := make([]string, 0, len(t.Generic))
		for _, a := range t.Generic {
			args = append(args, typeName(f, a))
		}
		return base + "<" + strings.Join(args, ", ") + ">"
	case winmd.KindArray:
		inner := ""
		if t.Inner != nil {
			inner = typeName(f, *t.Inner)
		}
		return inner + "[]"
	}
	return t.String()
}
