// Fail the CI build if npm's installed dependency tree differs from the reviewed overrides.
const fs = require('node:fs');
const path = require('node:path');
const manifest = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
const root = '/usr/local/lib/node_modules/npm';
if (require(path.join(root, 'package.json')).version !== manifest.version) throw Error('npm CLI version mismatch');
for (const [name, version] of Object.entries(manifest.overrides)) {
  const installed = require(path.join(root, 'node_modules', name, 'package.json')).version;
  if (installed !== version) throw Error(`Locked npm tooling mismatch for ${name}: ${installed}, expected ${version}`);
}
console.log('Locked npm tooling versions verified');
