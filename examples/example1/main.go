package main

import "embed"

//go:embed *.gotmpl
var tmpls embed.FS

func main() {}
