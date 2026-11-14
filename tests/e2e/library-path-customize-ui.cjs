// All APIs are intercepted: this can inspect a loopback preview without touching its data.
const assert = require('node:assert/strict');
const { chromium } = require('../../web/node_modules/playwright');

module.exports = async ({ browser, url }) => {
  const origin = new URL(url);
  if (!['localhost', '127.0.0.1'].includes(origin.hostname)) throw new Error('Requires a loopback fixture');
  const context = await browser.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'block', viewport: { width: 1280, height: 900 } });
  try {
    const list = (path) => ({ source: 'admin', default: [], paths: [{ path, status: 'ok', count: 219 }] });
    const folders = { movies: list('/media/movies'), tv: list('/media/tv'), commercials: list('/ads'), exclusions: { patterns: [], default: [], source: 'admin' } };
    const writes = [];
    const counts = { '/media/movies': { movies: 23, tv: 2, custom: 1 }, '/media/tv': { movies: 0, tv: 15, custom: 2 }, '/ads': { movies: 7, tv: 8, custom: 0 } };
    let failSave = false;
    let status = { revision: 0, appliedRevision: 0, state: 'idle', processed: 0, total: 0, error: '', needsScan: false };
    await context.route('**/*', async (route) => {
      const path = new URL(route.request().url());
      if (path.origin !== origin.origin) return route.abort();
      if (!path.pathname.startsWith('/api/')) return route.continue();
      const request = route.request();
      let body = {};
      if (request.method() !== 'GET' && path.pathname.startsWith('/api/channels')) throw new Error('Customize must not update individual channels');
      if (path.pathname === '/api/settings/folders/customize') {
        if (request.method() === 'GET') return route.fulfill({ json: counts[path.searchParams.get('path')] });
        assert.equal(request.method(), 'PUT');
        if (failSave) return route.fulfill({ status: 500, json: { error: 'Synthetic save failure' } });
        writes.push(request.postDataJSON());
        status = { ...status, revision: status.revision + 1, state: 'queued', error: '' };
        return route.fulfill({ status: 202, json: status });
      }
      if (path.pathname === '/api/settings/folders/updates/retry') {
        status = { ...status, revision: status.revision + 1, state: 'queued', error: '' };
        return route.fulfill({ status: 202, json: status });
      }
      if (path.pathname === '/api/settings/folders/updates') body = status;
      else if (path.pathname === '/api/auth/status') body = { authenticated: true, setupRequired: false, username: 'Fixture Owner', role: 'owner' };
      else if (path.pathname === '/api/profiles') body = [{ id: 1, name: 'Fixture Owner', role: 'admin', audienceTier: 'adult', hasPin: false, icon: null }];
      else if (path.pathname === '/api/settings/folders') body = folders;
      else if (path.pathname === '/api/settings/media') body = { pollIntervalMinutes: 60 };
      else if (path.pathname === '/api/settings/commercials') body = { enabled: true, mode: 'between-only', intervalMinutes: 30, tvCommercialsPerBreak: 2, movieCommercialsPerBreak: 5, volumeOffsetDb: 0 };
      else if (path.pathname === '/api/status/startup') body = { phase: 'ready', startupComplete: true, active: false, mediaRoots: 2, library: {} };
      await route.fulfill({ json: body });
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(new URL('/admin/library-paths', origin).href);
    const dialog = page.getByRole('dialog', { name: 'Customize Library Path' });
    const open = async (path) => { await page.getByRole('button', { name: `Customize ${path}`, exact: true }).click(); await dialog.waitFor(); };
    const defaults = async (movies, tv, custom) => {
      for (const [name, value] of [['Movie', movies], ['TV Show', tv], ['Custom', custom]]) assert.equal(await dialog.getByRole('switch', { name: `Enable for ${name} Channels`, exact: true }).getAttribute('aria-checked'), String(value));
    };
    const save = async () => { await dialog.getByRole('button', { name: 'Save', exact: true }).click(); await dialog.waitFor({ state: 'hidden' }); };
    await page.getByText('/media/movies (Found 219)', { exact: true }).waitFor();
    await open('/media/movies');
    await defaults(true, false, false);
    await dialog.getByText('(Channels 23)', { exact: true }).waitFor();
    await dialog.getByText('(Channels 2)', { exact: true }).waitFor();
    await dialog.getByText('(Channels 1)', { exact: true }).waitFor();
    const rightEdges = await dialog.locator('.path-customize-count').evaluateAll((labels) => labels.map((label) => label.getBoundingClientRect().right));
    assert.ok(Math.max(...rightEdges) - Math.min(...rightEdges) < 2, 'Channel counts align on the right');
    if (require.main === module) await page.screenshot({ path: '/tmp/alreadyon-customize-counts-desktop.png' });
    const desktop = await dialog.boundingBox();
    assert.ok(Math.abs(desktop.x + desktop.width / 2 - 640) < 2 && Math.abs(desktop.y + desktop.height / 2 - 450) < 2, 'Centered desktop modal');
    await dialog.getByRole('switch', { name: 'Enable for Custom Channels' }).click();
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    assert.equal(writes.length, 0);
    await open('/media/movies');
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    assert.equal(writes.length, 0);
    assert.ok(await page.getByRole('button', { name: 'Customize /media/movies', exact: true }).evaluate((button) => button === document.activeElement));
    await open('/media/tv');
    await defaults(false, true, false);
    await save();
    assert.deepEqual(writes, [{ kind: 'tv', path: '/media/tv', movies: false, tv: true, custom: false }]);
    await page.getByText(/Saved—channel updates queued/).waitFor();
    // Work remains queued throughout: editing another path cannot wait for it.
    await open('/ads');
    await defaults(true, true, false);
    await dialog.getByText('(Channels 7)', { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    const mobile = await dialog.boundingBox();
    assert.ok(mobile.x >= 0 && mobile.x + mobile.width <= 391 && Math.abs(mobile.y + mobile.height / 2 - 422) < 2);
    assert.ok(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth), 'Counts and labels fit the mobile modal');
    if (require.main === module) await page.screenshot({ path: '/tmp/alreadyon-customize-counts-mobile.png' });
    await save();
    assert.equal(writes.length, 2);
    assert.deepEqual(writes[1], { kind: 'commercials', path: '/ads', movies: true, tv: true, custom: false });
    failSave = true;
    await open('/media/movies');
    await dialog.getByRole('button', { name: 'Save', exact: true }).click();
    await dialog.getByRole('alert').waitFor();
    assert.match(await dialog.getByRole('alert').textContent(), /Synthetic save failure/);
    assert.equal(writes.length, 2, 'Rejected save is atomic');
    failSave = false;
    await save();
    status = { ...status, state: 'failed', error: 'Synthetic background failure.' };
    await page.evaluate(() => window.dispatchEvent(new Event('alreadyon-library-update')));
    await page.getByRole('alert').filter({ hasText: 'Your preferences are saved' }).waitFor();
    await page.getByRole('button', { name: 'Retry Update', exact: true }).click();
    await page.getByText(/Saved—channel updates queued/).waitFor();
    status = { ...status, state: 'updating', processed: 2, total: 40 };
    await page.evaluate(() => window.dispatchEvent(new Event('alreadyon-library-update')));
    await page.getByText('Saved—updating channels (2 of 40)…', { exact: true }).waitFor();
    status = { ...status, state: 'idle', appliedRevision: status.revision, processed: 40 };
    await page.evaluate(() => window.dispatchEvent(new Event('alreadyon-library-update')));
    await page.getByText('Library changes applied.', { exact: true }).waitFor();
    assert.deepEqual(errors, []);
    console.log('PASS: Customize defaults, single atomic requests, immediate close while queued, subsequent edits, save failure, background progress/retry, modal geometry/focus');
  } finally { await context.close(); }
};
if (require.main === module) {
  (async () => {
    const browser = await chromium.launch();
    try { await module.exports({ browser, url: process.argv[2] || 'https://localhost:5173' }); }
    finally { await browser.close(); }
  })().catch((error) => { console.error(error); process.exitCode = 1; });
}
