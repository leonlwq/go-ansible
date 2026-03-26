package main

import (
	"os"

	"go-ansible/pkg/cli"
)

const version = "0.1.0"

func main() {
	cli.Run(os.Args[1:], version)
}
