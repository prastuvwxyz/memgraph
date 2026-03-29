package config

// DefaultConfigTOML is the template written by `memgraph init`.
const DefaultConfigTOML = `# memgraph config
# https://github.com/prastuvwxyz/memgraph

# Number of results to return (default: 5)
top_n = 5

# Output format: "table", "json", or "paths"
format = "table"

# Patterns to exclude from indexing (gitignore-style globs)
exclude = [
  ".git",
  "node_modules",
  "vendor",
  "*.pdf",
  "*.csv",
]

# Named contexts for --ctx flag (optional)
# [contexts.work]
# root = "contexts/electrum"
#
# [contexts.personal]
# root = "."
`
