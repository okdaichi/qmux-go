//go:build mage

package main

import (
	"fmt"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// Default target to run when none is specified
var Default = All

// Build builds the project
func Build() error {
	fmt.Println("Building...")
	return sh.Run("go", "build", "./...")
}

// Test runs tests
func Test() error {
	fmt.Println("Testing...")
	return sh.Run("go", "test", "-v", "./...")
}

// Bench runs benchmarks
func Bench() error {
	fmt.Println("Benchmarking...")
	return sh.Run("go", "test", "-v", "-run=^$", "-bench", ".", "-benchmem", "./qmux")
}

// Fmt runs go fmt
func Fmt() error {
	fmt.Println("Formatting...")
	return sh.Run("go", "fmt", "./...")
}

// Tidy runs go mod tidy
func Tidy() error {
	fmt.Println("Tidying...")
	return sh.Run("go", "mod", "tidy")
}

// Coverage runs tests with coverage reporting
func Coverage() error {
	fmt.Println("Running tests with coverage...")
	if err := sh.Run("go", "test", "-coverprofile=coverage.out", "./..."); err != nil {
		return err
	}
	return sh.Run("go", "tool", "cover", "-html=coverage.out")
}

// Lint runs the linter (expects golangci-lint to be installed)
func Lint() error {
	fmt.Println("Linting...")
	return sh.Run("golangci-lint", "run")
}

// All runs Tidy, Fmt, Build, and Test
func All() {
	mg.SerialDeps(Tidy, Fmt, Build, Test)
}
