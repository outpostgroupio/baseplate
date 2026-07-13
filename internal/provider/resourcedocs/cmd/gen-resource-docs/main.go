// Command gen-resource-docs writes docs/resource-reference.md from the Roblox
// provider's resource schemas + Doc(). Invoked via `go generate ./...`.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/outpostgroupio/baseplate/internal/provider/resourcedocs"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox"
)

func main() {
	out := flag.String("out", "docs/resource-reference.md", "output path for the resource reference")
	flag.Parse()

	md, err := resourcedocs.Generate(roblox.SchemaProvider())
	if err != nil {
		log.Fatalf("generate: %v", err)
	}
	if err := os.WriteFile(*out, []byte(md), 0o644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
}
