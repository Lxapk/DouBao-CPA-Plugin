//go:build !cgo

// This file exists so the package builds and its tests run with
// CGO_ENABLED=0.
//
// The real entry point lives in cabi.go, which imports "C" and is therefore
// excluded from a cgo-free build. Without a main function here, `go test ./...`
// and `go vet ./...` would fail with "function main is undeclared in the main
// package" — the plugin could then only ever be tested with a C toolchain
// present, which is exactly the configuration CI is least likely to have.
//
// A build tagged this way does not produce a loadable plugin: CPA needs the
// C ABI. It is only for tests and static analysis.
package main

func main() {}
