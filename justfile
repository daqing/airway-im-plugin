dev: deps-setup
  overmind start -f Procfile.dev

# Materialize local go.mod files for the deps/ services (shipped as
# go.mod.templ so Go module zips keep them; never commit the copies).
deps-setup:
  cp -n deps/gateway/go.mod.templ deps/gateway/go.mod || true
  cp -n deps/delivery/go.mod.templ deps/delivery/go.mod || true

install:
  go install github.com/air-verse/air@latest
  brew install tmux
  brew install overmind

# Regenerate *_templ.go from the .templ views under backend/app/views.
generate:
  go generate ./...

# Regenerate the views, then keep them fresh while you edit .templ files.
generate-watch:
  cd backend && go tool templ generate -watch
