// Command render-templates renders the browser fixtures with the same Go
// html/template parser and source files used by the production HTTP handlers.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
)

func main() {
	if err := render(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func render() error {
	pages := make(map[string]string)
	for _, name := range []string{"index", "solana", "tron", "showcase"} {
		page, err := template.ParseFS(os.DirFS("internal/web"), "templates/"+name+".html")
		if err != nil {
			return err
		}
		variants := map[string]map[string]any{
			name + "-private": {"Shared": false},
			name + "-shared":  {"Shared": true},
		}
		switch name {
		case "index":
			variants = make(map[string]map[string]any)
			for _, native := range []string{"ETH", "POL"} {
				for _, mode := range []string{"private", "public", "shared"} {
					variants[name+"-"+mode+"-"+native] = map[string]any{
						"Native": native, "Public": mode == "public", "Shared": mode == "shared",
					}
				}
			}
		case "showcase":
			variants = map[string]map[string]any{"showcase": nil}
		}
		for key, data := range variants {
			var output bytes.Buffer
			if err := page.Execute(&output, data); err != nil {
				return fmt.Errorf("render %s: %w", key, err)
			}
			pages[key] = output.String()
		}
	}
	return json.NewEncoder(os.Stdout).Encode(pages)
}
