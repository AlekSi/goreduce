// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"flag"
	"fmt"
	"html/template"
	"os"
	"os/exec"

	"golang.org/x/tools/go/loader"

	"github.com/kisielk/gotool"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "Func name missing: goreduce funcName")
		os.Exit(1)
	}
	if err := reduce(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var testTmpl = template.Must(template.New("test").Parse(`` +
	`package {{ .Pkg }}

import "testing"

func TestReduce(t *testing.T) {
	if {{ .Expr }}() {
		t.Fail()
	}
}
`))

func writeTest(f *os.File, pkgName, expr string) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	return testTmpl.Execute(f, struct {
		Pkg, Expr string
	}{
		Pkg:  pkgName,
		Expr: expr,
	})
}

func reduce(funcName string) error {
	paths := gotool.ImportPaths([]string{"."})
	conf := loader.Config{}
	if _, err := conf.FromArgs(paths, false); err != nil {
		return err
	}
	prog, err := conf.Load()
	if err != nil {
		return err
	}
	pkgInfos := prog.InitialPackages()
	if len(pkgInfos) != 1 {
		return fmt.Errorf("expected 1 package, got %d", len(pkgInfos))
	}
	pkg := pkgInfos[0].Pkg
	f, err := os.Create("reduce_test.go")
	if err != nil {
		return err
	}
	if err := writeTest(f, pkg.Name(), funcName); err != nil {
		return err
	}
	if err := runTest(); err == nil {
		return fmt.Errorf("expected test to fail")
	}
	if err := writeTest(f, pkg.Name(), "!"+funcName); err != nil {
		return err
	}
	if err := runTest(); err != nil {
		return err
	}
	return nil
}

func runTest() error {
	return exec.Command("go", "test", "-run", "^TestReduce$").Run()
}
