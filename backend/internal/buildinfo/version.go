// Package buildinfo contains Alfred's build metadata.
package buildinfo

// Version is Alfred's build version. Release builds inject it with -ldflags -X.
//
//nolint:gochecknoglobals // The Go linker can replace only a package variable.
var Version = "dev"
