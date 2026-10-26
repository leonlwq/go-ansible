package main

import (
	"os"

	"go-ansible/pkg/cli"
)

var version = "0.1.0"

func main() {
	cli.RunVault(os.Args[1:], version)
}
