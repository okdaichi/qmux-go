//go:build mage

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func Build() error {
	return run("go", "build", "./...")
}

func Test() error {
	return run("go", "test", "./...")
}

func Fmt() error {
	return run("go", "fmt", "./...")
}

func Lint() error {
	fmt.Println("lint target is a stub")
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
