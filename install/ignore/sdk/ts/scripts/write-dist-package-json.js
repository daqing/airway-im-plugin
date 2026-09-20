// Emit the minimal package.json files the compiled outputs need:
// - dist/cjs: WeChat DevTools "Build npm" (packNpm) requires the directory
//   declared in the `miniprogram` field to carry its own package.json.
// - dist/esm: pins the module type so Node never reparses these ESM files
//   as CommonJS (MODULE_TYPELESS_PACKAGE_JSON warning).
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));

fs.mkdirSync(path.join(root, "dist/cjs"), { recursive: true });
fs.writeFileSync(
  path.join(root, "dist/cjs/package.json"),
  JSON.stringify(
    {
      name: pkg.name,
      version: pkg.version,
      description: pkg.description,
      main: "index.js",
    },
    null,
    2,
  ) + "\n",
);

fs.mkdirSync(path.join(root, "dist/esm"), { recursive: true });
fs.writeFileSync(path.join(root, "dist/esm/package.json"), JSON.stringify({ type: "module" }) + "\n");
