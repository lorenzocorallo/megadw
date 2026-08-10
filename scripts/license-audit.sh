#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO=${GO:-go}
VP=${VP:-vp}

cd "$ROOT"
test -f LICENSE
test -f NOTICE.md
"$GO" mod verify

missing=0
for module_dir in $($GO list -m -f '{{if not .Main}}{{.Dir}}{{end}}' all); do
	if ! find "$module_dir" -maxdepth 2 -type f \( \
		-iname 'license' -o -iname 'license.*' -o -iname 'copying' -o -iname 'copying.*' -o \
		-iname 'unlicense' -o -iname 'patents' \) -print -quit | grep -q .; then
		echo "Go module has no discoverable license file: $module_dir" >&2
		missing=$((missing + 1))
	fi
done
if [ "$missing" -ne 0 ]; then
	exit 1
fi

cd "$ROOT/web"
"$VP" node --input-type=module -e '
import fs from "node:fs";
const lock = JSON.parse(fs.readFileSync("package-lock.json", "utf8"));
const packageJson = JSON.parse(fs.readFileSync("package.json", "utf8"));
const registryLicenseOverrides = new Map([
  ["@yuku-codegen/binding-darwin-arm64", "MIT"],
  ["@yuku-codegen/binding-darwin-x64", "MIT"],
  ["@yuku-codegen/binding-freebsd-x64", "MIT"],
  ["@yuku-codegen/binding-linux-arm-gnu", "MIT"],
  ["@yuku-codegen/binding-linux-arm-musl", "MIT"],
  ["@yuku-codegen/binding-linux-arm64-gnu", "MIT"],
  ["@yuku-codegen/binding-linux-arm64-musl", "MIT"],
  ["@yuku-codegen/binding-linux-x64-gnu", "MIT"],
  ["@yuku-codegen/binding-linux-x64-musl", "MIT"],
  ["@yuku-codegen/binding-win32-arm64", "MIT"],
  ["@yuku-codegen/binding-win32-x64", "MIT"],
  ["@yuku-parser/binding-darwin-arm64", "MIT"],
  ["@yuku-parser/binding-darwin-x64", "MIT"],
  ["@yuku-parser/binding-freebsd-x64", "MIT"],
  ["@yuku-parser/binding-linux-arm-gnu", "MIT"],
  ["@yuku-parser/binding-linux-arm-musl", "MIT"],
  ["@yuku-parser/binding-linux-arm64-gnu", "MIT"],
  ["@yuku-parser/binding-linux-arm64-musl", "MIT"],
  ["@yuku-parser/binding-linux-x64-gnu", "MIT"],
  ["@yuku-parser/binding-linux-x64-musl", "MIT"],
  ["@yuku-parser/binding-win32-arm64", "MIT"],
  ["@yuku-parser/binding-win32-x64", "MIT"],
]);
const missing = Object.entries(lock.packages ?? {}).filter(([path, value]) => {
  if (!path.startsWith("node_modules/")) return false;
  const name = path.slice("node_modules/".length);
  return !value || (typeof value.license !== "string" || value.license.trim() === "") && !registryLicenseOverrides.has(name);
});
if (missing.length) {
  console.error(`npm packages without lockfile license metadata: ${missing.map(([name]) => name).join(", ")}`);
  process.exit(1);
}
const expectedDirectLicenses = new Map([
  ["@fontsource-variable/geist", "OFL-1.1"],
  ["class-variance-authority", "Apache-2.0"],
  ["@playwright/test", "Apache-2.0"],
  ["typescript", "Apache-2.0"],
  ["lucide-react", "ISC"],
]);
const direct = [...Object.keys(packageJson.dependencies ?? {}), ...Object.keys(packageJson.devDependencies ?? {})];
const directIssues = direct.flatMap((name) => {
  const license = lock.packages[`node_modules/${name}`]?.license;
  const expected = expectedDirectLicenses.get(name) ?? "MIT";
  return license !== expected ? [`${name}: expected ${expected}, found ${license ?? "missing"}`] : [];
});
if (directIssues.length) {
  console.error(`direct npm license inventory mismatch: ${directIssues.join(", ")}`);
  process.exit(1);
}
console.log(`npm lockfile license metadata: PASS (${Object.keys(lock.packages ?? {}).length} package records)`);
console.log(`npm direct license inventory: PASS (${direct.length} packages)`);
console.log(`npm registry license overrides checked: ${registryLicenseOverrides.size} optional native records (MIT)`);
'

echo 'dependency/license audit: PASS'
