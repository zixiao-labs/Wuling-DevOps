package insightstore

import (
	"path"
	"strings"
)

// LanguageFromFilename returns the canonical language name for a filename, or
// "" when no rule matches. We resolve in order: full-basename match (handles
// Dockerfile / Makefile), then file extension. Extension-only matching is
// good enough for Stage 1; full linguist parity is explicitly deferred.
//
// The list is intentionally short — covering ~the top of GitHub's list — so
// the response surface stays small. Add entries as projects need them.
func LanguageFromFilename(name string) string {
	if name == "" {
		return ""
	}
	base := path.Base(name)
	if lang, ok := basenames[strings.ToLower(base)]; ok {
		return lang
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		if lang, ok := extensions[strings.ToLower(base[i:])]; ok {
			return lang
		}
	}
	return ""
}

var basenames = map[string]string{
	"dockerfile":  "Dockerfile",
	"makefile":    "Makefile",
	"gnumakefile": "Makefile",
	"justfile":    "Just",
	"rakefile":    "Ruby",
	"gemfile":     "Ruby",
}

var extensions = map[string]string{
	".go":     "Go",
	".rs":     "Rust",
	".py":     "Python",
	".js":     "JavaScript",
	".mjs":    "JavaScript",
	".cjs":    "JavaScript",
	".jsx":    "JavaScript",
	".ts":     "TypeScript",
	".tsx":    "TypeScript",
	".rb":     "Ruby",
	".java":   "Java",
	".kt":     "Kotlin",
	".kts":    "Kotlin",
	".swift":  "Swift",
	".c":      "C",
	".h":      "C",
	".cc":     "C++",
	".cpp":    "C++",
	".cxx":    "C++",
	".hpp":    "C++",
	".hh":     "C++",
	".m":      "Objective-C",
	".mm":     "Objective-C++",
	".cs":     "C#",
	".vb":     "Visual Basic .NET",
	".fs":     "F#",
	".scala":  "Scala",
	".clj":    "Clojure",
	".ex":     "Elixir",
	".exs":    "Elixir",
	".erl":    "Erlang",
	".hs":     "Haskell",
	".ml":     "OCaml",
	".lua":    "Lua",
	".php":    "PHP",
	".pl":     "Perl",
	".sh":     "Shell",
	".bash":   "Shell",
	".zsh":    "Shell",
	".fish":   "Shell",
	".ps1":    "PowerShell",
	".bat":    "Batchfile",
	".cmd":    "Batchfile",
	".sql":    "SQL",
	".md":     "Markdown",
	".markdown": "Markdown",
	".rst":    "reStructuredText",
	".tex":    "TeX",
	".html":   "HTML",
	".htm":    "HTML",
	".css":    "CSS",
	".scss":   "SCSS",
	".sass":   "Sass",
	".less":   "Less",
	".vue":    "Vue",
	".svelte": "Svelte",
	".yaml":   "YAML",
	".yml":    "YAML",
	".toml":   "TOML",
	".json":   "JSON",
	".jsonc":  "JSON with Comments",
	".json5":  "JSON5",
	".xml":    "XML",
	".proto":  "Protocol Buffers",
	".dockerfile": "Dockerfile",
	".tf":     "HCL",
	".hcl":    "HCL",
	".gradle": "Gradle",
	".dart":   "Dart",
	".zig":    "Zig",
	".nim":    "Nim",
	".cr":     "Crystal",
	".sol":    "Solidity",
	".vim":    "Vim Script",
	".r":      "R",
	".jl":     "Julia",
	".ipynb":  "Jupyter Notebook",
}
