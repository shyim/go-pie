package main

import (
	"os"

	"github.com/shyim/go-pie/internal/commands"
)

func main() {
	os.Exit(commands.Execute())
}
