//go:build never

// Temporary placeholder so `go mod tidy` materializes go.sum for the pinned
// dependencies before any package code exists. Removed during integration.
package main

import (
	_ "github.com/golang/snappy"
	_ "github.com/sigstore/sigstore-go/pkg/verify"
	_ "github.com/spf13/cobra"
	_ "github.com/spf13/pflag"
	_ "golang.org/x/sys/unix"
	_ "golang.org/x/term"
)
