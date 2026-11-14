// Real browser/API persistence against playback.sh's marked disposable fixture.
// Restore all touched channel filters and commercial overrides before returning.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

module.exports = async ({ browser, login, url, root }) => {
  assert.ok(root && fs.existsSync(path.join(root, '.synthetic-test')), 'Requires isolated playback fixture');
  assert.equal(url, process.env.PLAYBACK_TEST_URL, 'Requires the harness-owned server URL');
  const context = await browser.newContext();
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await login(page, "/admin/library-paths", ".folder-card-header h3");
  const request = async (method, endpoint, data) => {
    const response = await context.request.fetch(url + endpoint, { method, ...(data === undefined ? {} : { data }) });
    assert.ok(response.ok(), `${method} ${endpoint}: ${response.status()} ${await response.text()}`);
    return response.status() === 204 ? null : response.json();
  };
  const originalFolders = await request('GET', '/api/settings/folders');
  const originalChannels = await request('GET', '/api/channels');
  const originalCommercials = new Map();
  for (const channel of originalChannels) originalCommercials.set(channel.id, await request('GET', `/api/channels/${channel.id}/commercials`));
  const moviePath = path.join(root, 'customize', 'Movies');
  const tvPath = path.join(root, 'customize', 'TV');
  const adPath = path.join(root, 'customize', 'Ads');
  const otherAdPath = path.join(root, 'customize', 'Other Ads');
  const removePath = path.join(root, 'customize', 'Remove Me');
  for (const folder of [moviePath, tvPath, adPath, otherAdPath, removePath]) fs.mkdirSync(folder, { recursive: true });
  let custom;
  const group = (channel) => channel.number < 100 || channel.number > 199 ? 'custom' : channel.filter?.mediaType === 'movie' ? 'movies' : 'tv';
  const choices = async (expected) => {
    for (const [label, value] of [['Movie', expected.movies], ['TV Show', expected.tv], ['Custom', expected.custom]]) {
      assert.equal(await page.getByRole('dialog').getByRole('switch', { name: `Enable for ${label} Channels`, exact: true }).getAttribute('aria-checked'), String(value));
    }
  };
  const open = async (folder) => {
    await page.goto(url + '/admin/library-paths');
    await page.getByRole('button', { name: `Customize ${folder}`, exact: true }).click();
    await page.getByRole('dialog', { name: 'Customize Library Path' }).waitFor();
  };
  const save = async () => {
    await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached', timeout: 60000 });
  };
  try {
    await request('PUT', '/api/settings/folders', { movies: [moviePath, removePath], tv: [tvPath], commercials: [adPath, otherAdPath] });
    // Wait for real pipeline completion so lazy default filters exist before bulk saves.
    const profiles = await request('GET', '/api/profiles');
    await request('POST', '/api/admin/refresh-media', { profileId: profiles.find((profile) => profile.role === 'admin').id });
    const movie = originalChannels.find((channel) => channel.number === 133);
    const tv = originalChannels.find((channel) => channel.number === 121);
    assert.ok(movie && tv, 'Built-in movie and TV channel rows exist');
    // An empty startup library has channel rows but may not have filters yet.
    const base = { audienceTier: 'adult', genres: [], mediaType: 'movie', era: 'both', titleContains: '' };
    const builtinMovie = { ...movie, filter: movie.filter ?? base };
    const builtinTV = { ...tv, filter: tv.filter ?? { ...base, mediaType: 'tv' } };
    custom = await request('POST', '/api/channels', { number: 9902, name: 'Path Custom Movie', filter: { ...builtinMovie.filter, includedPaths: [tvPath], excludedPaths: [] } });
    const baseline = new Map([[custom.id, custom]]);
    // Deliberately start with opposite selections so Save must overwrite them.
    for (const channel of [builtinMovie, builtinTV]) {
      baseline.set(channel.id, await request('PUT', `/api/channels/${channel.id}`, { number: channel.number, name: channel.name, filter: { ...channel.filter, includedPaths: [tvPath], excludedPaths: [] } }));
    }
    await open(moviePath);
    await choices({ movies: true, tv: false, custom: false });
    await page.getByRole('dialog').getByRole('switch', { name: 'Enable for Custom Channels' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click();
    let current = await request('GET', '/api/channels');
    for (const channel of [builtinMovie, builtinTV, custom]) assert.deepEqual(current.find((c) => c.id === channel.id).filter.includedPaths, [tvPath], 'Cancel preserves saved choices');
    await open(moviePath);
    await save();
    current = await request('GET', '/api/channels');
    for (const channel of current.filter((c) => c.filter)) {
      assert.equal(channel.filter.includedPaths.includes(moviePath), group(channel) === 'movies', `Movie path group for ${channel.number}`);
    }
    for (const channel of [builtinMovie, builtinTV, custom]) {
      const saved = current.find((c) => c.id === channel.id);
      assert.ok(saved.filter.includedPaths.includes(tvPath), 'Other media path preserved');
      const { includedPaths, excludedPaths, ...rest } = saved.filter;
      const { includedPaths: oldIncluded, excludedPaths: oldExcluded, ...oldRest } = baseline.get(channel.id).filter;
      assert.deepEqual(rest, oldRest, 'Other filter rules preserved');
      assert.equal(saved.name, channel.name);
    }
    await open(tvPath);
    await choices({ movies: false, tv: true, custom: false });
    await page.getByRole('dialog').getByRole('switch', { name: 'Enable for Custom Channels' }).click();
    await save();
    current = await request('GET', '/api/channels');
    for (const channel of current.filter((c) => c.filter)) {
      assert.equal(channel.filter.includedPaths.includes(tvPath), group(channel) !== 'movies', `TV path group for ${channel.number}`);
      assert.equal(channel.filter.includedPaths.includes(moviePath), group(channel) === 'movies', 'Movie selection survives TV save');
    }
    // Reopen the channel editor after a full page navigation to verify the saved checkbox.
    await page.goto(url + '/admin/channels');
    await page.getByRole('button', { name: `Edit ${custom.name}`, exact: true }).click();
    assert.equal(await page.getByRole('dialog').getByRole('switch', { name: tvPath, exact: true }).getAttribute('aria-checked'), 'true');
    assert.equal(await page.getByRole('dialog').getByRole('switch', { name: moviePath, exact: true }).getAttribute('aria-checked'), 'false');
    await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click();
    await open(adPath);
    await choices({ movies: true, tv: true, custom: false });
    await save();
    for (const channel of current.filter((c) => c.filter || c.number < 100 || c.number > 199)) {
      const policy = await request('GET', `/api/channels/${channel.id}/commercials`);
      assert.equal(policy.effectiveFolders.includes(adPath), group(channel) !== 'custom', `Commercial path group for ${channel.number}`);
      assert.ok(policy.effectiveFolders.includes(otherAdPath), 'Other commercial folder preserved');
      if (originalCommercials.has(channel.id)) assert.equal(policy.enabled, originalCommercials.get(channel.id).enabled, 'Break enablement preserved');
    }
    // Disable the last remaining custom-channel commercial folder: [] means none.
    await open(otherAdPath);
    await save();
    let policy = await request('GET', `/api/channels/${custom.id}/commercials`);
    assert.equal(policy.explicitFolders, true);
    assert.deepEqual(policy.effectiveFolders, []);
    await page.reload();
    policy = await request('GET', `/api/channels/${custom.id}/commercials`);
    assert.deepEqual(policy.effectiveFolders, [], 'Empty commercial selection persists after reload');
    const row = page.locator('.folder-list-item').filter({ hasText: removePath });
    await row.getByRole('button', { name: 'Remove', exact: true }).click();
    await page.waitForFunction(async (folder) => {
      const settings = await (await fetch('/api/settings/folders')).json();
      return !settings.movies.paths.some((p) => p.path === folder);
    }, removePath);
    assert.deepEqual(errors, []);
    console.log('PASS: Customize real API persistence, defaults, cancel, all channel groups, unrelated selections, explicit empty commercials, reload and Remove');
  } finally {
    if (custom) await request('DELETE', `/api/channels/${custom.id}`);
    await request('PUT', '/api/settings/folders', Object.fromEntries(['movies', 'tv', 'commercials'].map((kind) => [kind, originalFolders[kind].source === 'default' ? null : originalFolders[kind].paths.map((p) => p.path)])));
    for (const channel of originalChannels) {
      // Demo rows are transient and are excluded from Customize itself.
      if (channel.number >= 196 && channel.number <= 199) continue;
      await request('PUT', `/api/channels/${channel.id}`, { number: channel.number, name: channel.name, filter: channel.filter });
      const policy = originalCommercials.get(channel.id);
      await request('PUT', `/api/channels/${channel.id}/commercials`, { enabled: policy.enabled, folders: policy.folders, explicitFolders: !!policy.explicitFolders });
    }
    await context.close();
  }
};
