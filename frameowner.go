package nightwatch

import (
	"runtime/debug"
	"strings"
)

// frameOwner says whose code a stack frame is.
type frameOwner int

const (
	ownedByGo frameOwner = iota // the Go installation
	ownedByApp
	ownedByDependency
)

// mainModule is the module path of the running program, or "" when its build
// information does not say (a GOPATH build, go run of single files).
func mainModule() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Path == "command-line-arguments" {
		return ""
	}

	return info.Main.Path
}

// ownerOf decides from the function name when the main module is known: its
// packages (and their tests, and package main) are the application, a package
// whose path starts with a domain is a dependency, the rest is Go itself. A
// build in /go/src/app or with -trimpath is then read right. Without the
// module, the file path decides.
func ownerOf(module, function, file string) frameOwner {
	switch {
	case module == "" || function == "":
		return ownerOfFile(file)
	case inModule(function, module):
		return ownedByApp
	case standardLibrary(function):
		return ownedByGo
	default:
		return ownedByDependency
	}
}

func ownerOfFile(file string) frameOwner {
	switch {
	case isApplicationFile(file):
		return ownedByApp
	case isDependencyFile(file):
		return ownedByDependency
	default:
		return ownedByGo
	}
}

// inModule reports whether a function ("pkg/path.(*T).Method") belongs to a
// package of module, or of package main.
func inModule(function, module string) bool {
	if strings.HasPrefix(function, "main.") {
		return true
	}

	rest, found := strings.CutPrefix(function, module)
	if !found {
		return false
	}

	rest = strings.TrimPrefix(rest, "_test") // the module's external test package

	// A package below the module, or the root package itself (whose symbols
	// hold no "/"; "example.com/app.v2/x" is not in "example.com/app").
	return strings.HasPrefix(rest, "/") || (strings.HasPrefix(rest, ".") && !strings.Contains(rest, "/"))
}

// standardLibrary reports whether a function's package path has no domain in
// its first element ("net/http", "runtime"), as the standard library has.
func standardLibrary(function string) bool {
	first, _, nested := strings.Cut(function, "/")

	return !nested || !strings.Contains(first, ".")
}

// frameFunction is the function of an exception frame's Source, without its
// arguments: "pkg.(*T).M(...)" gives "pkg.(*T).M".
func frameFunction(source string) string {
	if open := strings.LastIndexByte(source, '('); open > 0 && strings.HasSuffix(source, ")") {
		return source[:open]
	}

	return source
}
