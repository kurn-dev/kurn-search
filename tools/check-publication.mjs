import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const digest = raw => createHash('sha256').update(raw).digest('hex');
const read = name => readFileSync(join(root, name));
const fail = message => { throw Error(message); };
const expected = [
  ['openapi/current-manifest.json', 'openapi', '5ec664a4331bfdc2000fee3eec9b08efcbf965a8370f3f64a961d321c32649af'],
  ['clients/current-manifest.json', 'clients', 'ab3651305090ab05160b3c2c3bf19929b06fefb6225e90790f1a1ca425761529'],
  ['adapter/analyzer/testdata/semantic-identity.json', 'analyzer', 'e729a51817550164438841fafc650086e41ec86f43d3af13d266319ee6e6f285'],
];
function checkSummary(value, kind, upstreamHash) {
  const keys = Object.keys(value).sort();
  if (JSON.stringify(keys) !== JSON.stringify(['evidence', 'kind', 'public_artifacts', 'schema', 'scope', 'upstream']) ||
    value.schema !== 'kurn-search-public-provenance-summary/v1' || value.kind !== kind ||
    value.upstream.sha256 !== upstreamHash || Object.keys(value.upstream).sort().join(',') !== 'schema,sha256')
    fail('unexpected public summary shape: ' + kind);
  function checkMetadata(object) {
    if (!object || typeof object !== 'object') return;
    for (const [key, item] of Object.entries(object)) {
      if (['source_files', 'sources', 'generator', 'command', 'base_commit', 'base_tree', 'base_head', 'source'].includes(key))
        fail('source-layout metadata is not permitted in summaries');
      checkMetadata(item);
    }
  }
  checkMetadata(value);
}
let artifactCount = 0;
for (const [name, kind, upstreamHash] of expected) {
  const summary = JSON.parse(read(name));
  checkSummary(summary, kind, upstreamHash);
  for (const item of summary.public_artifacts) {
    if (!/^[A-Za-z0-9_.\/-]+$/.test(item.name) || item.name.split('/').some(part => !part || part === '.' || part === '..') ||
      !lstatSync(join(root, item.name)).isFile()) fail('public artifact path refused');
    const raw = read(item.name);
    if (raw.length !== item.bytes || digest(raw) !== item.sha256) fail('public artifact identity mismatch: ' + item.name);
    artifactCount++;
  }
}
const analyzerHash = expected[2][2];
for (const adapter of ['catalogid', 'partscatalog', 'partscatalogselect', 'partscatalogdiscover',
  'partscatalogfacets', 'partscatalogsuggest']) {
  if (!read('adapters/' + adapter + '/profile.go').toString().includes('analyzerOperationalIdentitySHA256 = "' + analyzerHash + '"'))
    fail('upstream analyzer identity changed: ' + adapter);
}
if (!read('adapters/partscatalogalternatives/profile.go').toString().includes('base, err := partscatalogfacets.NewDescriptor(binding)'))
  fail('facet alternatives no longer inherit the checked analyzer authority');
const inventory = JSON.parse(read('release-inventory.json'));
for (const item of inventory.files) {
  const raw = read(item.name);
  if (raw.length !== item.bytes || digest(raw) !== item.sha256) fail('selected source inventory mismatch: ' + item.name);
}
function git(args) {
  return spawnSync('git', args, { cwd: root, encoding: 'utf8' });
}
let historyCommits = null;
const top = git(['rev-parse', '--show-toplevel']);
if (top.status === 0 && resolve(top.stdout.trim()) === root) {
  const history = git(['rev-list', '--all']);
  if (history.status !== 0) fail('public history unavailable');
  const commits = history.stdout.trim().split('\n').filter(Boolean);
  for (const commit of commits) {
    for (const [name, kind, upstreamHash] of expected) {
      const blob = git(['show', commit + ':' + name]);
      if (blob.status !== 0) fail('public history summary missing');
      checkSummary(JSON.parse(blob.stdout), kind, upstreamHash);
    }
  }
  historyCommits = commits.length;
}
console.log(JSON.stringify({ result: 'PASS', public_summaries: expected.length, verified_artifacts: artifactCount,
  selected_files: inventory.files.length, upstream_analyzer_identity_preserved: true,
  filtered_public_history_commits: historyCommits }, null, 2));
