import assert from 'node:assert/strict';
import { test } from 'node:test';
import { selectedChannelPaths, setChannelPath } from '../src/pages/admin/channelPaths.ts';

const paths = ['/media/movies', '/media/tv', '/media/tv-extra'];
const filter = { audienceTier: 'adult', genres: ['comedy'], mediaType: 'both', era: 'both', titleContains: 'holiday' } as const;
const base = { ...filter, genres: [...filter.genres] };

test('disabling from implicit all preserves other paths and filter fields', () => {
  assert.deepEqual(setChannelPath(base, paths, paths[0], false), {
    ...base, includedPaths: paths.slice(1), excludedPaths: [],
  });
  assert.equal('includedPaths' in base, false);
});

test('explicit empty selections stay empty and enabling is idempotent', () => {
  const empty = { ...base, includedPaths: [] };
  assert.deepEqual(selectedChannelPaths(empty, paths), []);
  const enabled = setChannelPath(empty, paths, paths[0], true);
  assert.deepEqual(enabled.includedPaths, [paths[0]]);
  assert.deepEqual(setChannelPath(enabled, paths, paths[0], true), enabled);
  assert.deepEqual(setChannelPath(enabled, paths, paths[0], false).includedPaths, []);
});

test('legacy exclusions respect directory boundaries and enable overrides exclusion', () => {
  const legacy = { ...base, excludedPaths: ['/media/tv/'] };
  assert.deepEqual(selectedChannelPaths(legacy, paths), [paths[0], paths[2]]);
  assert.deepEqual(setChannelPath(legacy, paths, paths[1], true).includedPaths, [paths[0], paths[2], paths[1]]);
});

test('unlisted explicit selections survive changes to another path', () => {
  const custom = { ...base, includedPaths: ['/old/path', paths[0]] };
  assert.deepEqual(setChannelPath(custom, paths, paths[0], false).includedPaths, ['/old/path']);
});
