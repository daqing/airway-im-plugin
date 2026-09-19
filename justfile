# Materialize local go.mod files for the deps/ services (shipped as
# go.mod.templ so Go module zips keep them; never commit the copies).
deps-setup:
  cp -n install/deps/im/gateway/go.mod.templ install/deps/im/gateway/go.mod || true
  cp -n install/deps/im/delivery/go.mod.templ install/deps/im/delivery/go.mod || true
