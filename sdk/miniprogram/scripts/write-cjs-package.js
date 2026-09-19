// WeChat DevTools 构建 npm (packNpm): when a package declares the
// `miniprogram` field, the packed directory must contain its own
// package.json. Emit a minimal one next to the compiled CJS output.
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");
const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
const out = {
  name: pkg.name,
  version: pkg.version,
  description: pkg.description,
  main: "index.js",
};
fs.mkdirSync(path.join(root, "dist/cjs"), { recursive: true });
fs.writeFileSync(path.join(root, "dist/cjs/package.json"), JSON.stringify(out, null, 2) + "\n");
