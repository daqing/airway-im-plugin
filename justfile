# Materialize local go.mod files for the deps/ services (shipped as
# go.mod.templ so Go module zips keep them; never commit the copies).
deps-setup:
  cp -n deps/gateway/go.mod.templ deps/gateway/go.mod || true
  cp -n deps/delivery/go.mod.templ deps/delivery/go.mod || true
