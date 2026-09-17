# Materialize local go.mod files for the deps/ services (shipped as
# go.mod.templ so Go module zips keep them; never commit the copies).
deps-setup:
  cp -n deps/im/gateway/go.mod.templ deps/im/gateway/go.mod || true
  cp -n deps/im/delivery/go.mod.templ deps/im/delivery/go.mod || true
