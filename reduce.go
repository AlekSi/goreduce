// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

const (
	testFile = "goreduce_test.go"
	testName = "TestReduce"
)

var testTmpl = template.Must(template.New("test").Parse(`` +
	`package {{ .Pkg }}

import "testing"

func {{ .TestName }}(t *testing.T) {
	{{ .Func }}()
}
`))

func emptyFile(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.Seek(0, 0)
	return err
}

type reducer struct {
	impPath string
	matchRe *regexp.Regexp

	fset     *token.FileSet
	pkg      *ast.Package
	files    []*ast.File
	file     *ast.File
	funcDecl *ast.FuncDecl
	srcFile  *os.File

	tinfo types.Config

	wd        string
	didChange bool
	stmt      *ast.Stmt
	expr      *ast.Expr
}

func reduce(impPath, funcName, matchStr string) error {
	r := &reducer{impPath: impPath}
	var err error
	if r.wd, err = os.Getwd(); err != nil {
		return err
	}
	r.tinfo.Importer = importer.Default()
	if r.matchRe, err = regexp.Compile(matchStr); err != nil {
		return err
	}
	r.fset = token.NewFileSet()
	pkgs, err := parser.ParseDir(r.fset, impPath, nil, 0)
	if err != nil {
		return err
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("expected 1 package, got %d", len(pkgs))
	}
	for _, pkg := range pkgs {
		r.pkg = pkg
	}
	for _, file := range r.pkg.Files {
		r.files = append(r.files, file)
	}
	r.file, r.funcDecl = findFunc(r.files, funcName)
	if r.file == nil {
		return fmt.Errorf("top-level func %s does not exist", funcName)
	}
	fname := r.fset.Position(r.file.Pos()).Filename
	testFilePath := filepath.Join(filepath.Dir(fname), testFile)
	tf, err := os.Create(testFilePath)
	if err != nil {
		return err
	}
	defer func() {
		tf.Close()
		os.Remove(testFilePath)
	}()
	// Check that it compiles and the output matches before we apply
	// any changes
	if err := testTmpl.Execute(tf, struct {
		Pkg, TestName, Func string
	}{
		Pkg:      r.pkg.Name,
		TestName: testName,
		Func:     funcName,
	}); err != nil {
		return err
	}
	if err := r.checkTest(); err != nil {
		return err
	}
	if r.srcFile, err = os.Create(fname); err != nil {
		return err
	}
	for err == nil {
		if err = r.step(); err == errNoChange {
			err = nil
			break // we're done
		}
	}
	if err2 := r.srcFile.Close(); err == nil && err2 != nil {
		return err2
	}
	return err
}

func (r *reducer) logChange(node ast.Node, format string, a ...interface{}) {
	if *verbose {
		pos := r.fset.Position(node.Pos())
		rpath, err := filepath.Rel(r.wd, pos.Filename)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "%s:%d: %s\n", rpath, pos.Line,
			fmt.Sprintf(format, a...))
	}
}

func (r *reducer) checkTest() error {
	err := runTest(r.impPath)
	if err == nil {
		return fmt.Errorf("expected an error to occur")
	}
	if s := err.Error(); !r.matchRe.MatchString(s) {
		return fmt.Errorf("error does not match:\n%s", s)
	}
	return nil
}

var errNoChange = fmt.Errorf("no reduction to apply")

func (r *reducer) writeSource() error {
	if err := emptyFile(r.srcFile); err != nil {
		return err
	}
	return printer.Fprint(r.srcFile, r.fset, r.file)
}

func (r *reducer) okChange() bool {
	if r.didChange {
		return false
	}
	// go/types catches most compile errors before writing
	// to disk and running the go tool. Since quite a lot of
	// changes are nonsensical, this is often a big win.
	if _, err := r.tinfo.Check(r.impPath, r.fset, r.files, nil); err != nil {
		terr, ok := err.(types.Error)
		if ok && terr.Soft && r.shouldRetry(terr) {
			return r.okChange()
		}
		return false
	}
	if err := r.writeSource(); err != nil {
		return false
	}
	if err := r.checkTest(); err != nil {
		return false
	}
	// Reduction worked
	r.didChange = true
	return true
}

var (
	importNotUsed = regexp.MustCompile(`"(.*)" imported but not used`)
)

func (r *reducer) shouldRetry(terr types.Error) bool {
	if sm := importNotUsed.FindStringSubmatch(terr.Msg); sm != nil {
		name, path := "", sm[1]
		for _, imp := range r.file.Imports {
			if imp.Name != nil && strings.Trim(imp.Path.Value, `"`) == path {
				name = imp.Name.Name
				break
			}
		}
		return astutil.DeleteNamedImport(r.fset, r.file, name, path)
	}
	return false
}

func (r *reducer) step() error {
	r.didChange = false
	r.walk(r.file, func(v interface{}) bool {
		if r.didChange {
			return false
		}
		return r.reduceNode(v)
	})
	if r.didChange {
		return nil
	}
	if err := r.writeSource(); err != nil {
		return err
	}
	return errNoChange
}

func findFunc(files []*ast.File, name string) (*ast.File, *ast.FuncDecl) {
	for _, file := range files {
		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if ok && funcDecl.Name.Name == name {
				return file, funcDecl
			}
		}
	}
	return nil, nil
}

func runTest(impPath string) error {
	cmd := exec.Command("go", "test", impPath, "-run", "^"+testName+"$")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), "exit status") {
		return errors.New(string(out))
	}
	return err
}
