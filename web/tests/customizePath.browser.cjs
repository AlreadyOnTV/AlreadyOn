// Run against the managed loopback preview. Every API request is intercepted:
// fixtures and captured writes never reach the preview database.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'block', viewport: { width: 1280, height: 900 } });
    const list = (path) => ({ source: 'admin', default: [], paths: [{ path, status: 'ok', count: 219 }] });
    const folders = { movies: list('/media/movies'), tv: list('/media/tv'), commercials: list('/ads'), exclusions: { patterns: [], default: [], source: 'admin' } };
    const filter = { audienceTier: 'adult', genres: ['comedy'], mediaType: 'movie', era: 'both', titleContains: '' };
    const channels = [
      { id: 1, number: 133, name: 'Movies', filter },
      { id: 2, number: 121, name: 'TV', filter: { ...filter, mediaType: 'tv' } },
      { id: 3, number: 200, name: 'Custom movie channel', filter },
      { id: 4, number: 196, name: 'Demo', filter: null },
    ];
    const writes = [];
    let failTV = false;
    await context.route('**/*', async (route) => {
      const url = new URL(route.request().url());
      if (!['localhost', '127.0.0.1'].includes(url.hostname)) return route.abort();
      if (!url.pathname.startsWith('/api/')) return route.continue();
      const request = route.request();
      let body = {};
      if (request.method() !== 'GET') {
        if (url.pathname.startsWith('/api/channels/')) writes.push({ path: url.pathname, body: request.postDataJSON() });
        if (failTV && url.pathname === '/api/channels/2') return route.fulfill({ status: 500, json: { error: 'Synthetic failure' } });
      }
      if (url.pathname === '/api/auth/status') body = { authenticated: true, setupRequired: false, username: 'Fixture Owner', role: 'owner' };
      else if (url.pathname === '/api/profiles') body = [{ id: 1, name: 'Fixture Owner', role: 'admin', audienceTier: 'adult', hasPin: false, icon: null }];
      else if (url.pathname === '/api/settings/folders') body = folders;
      else if (url.pathname === '/api/settings/media') body = { pollIntervalMinutes: 60 };
      else if (url.pathname === '/api/settings/commercials') body = { enabled: true, mode: 'between-only', intervalMinutes: 30, tvCommercialsPerBreak: 2, movieCommercialsPerBreak: 5, volumeOffsetDb: 0 };
      else if (url.pathname === '/api/channels') body = channels;
      else if (/\/api\/channels\/\d+\/commercials$/.test(url.pathname)) body = { enabled: null, effectiveEnabled: true, globalEnabled: true, folders: [], availableFolders: ['/ads'], effectiveFolders: ['/ads'], foldersFallback: false };
      else if (url.pathname === '/api/status/startup') body = { phase: 'ready', startupComplete: true, active: false, mediaRoots: 2, library: {} };
      await route.fulfill({ json: body });
    });
    const page = await context.newPage();
    page.on('pageerror', (err) => console.error('Browser error:', err.message));
    await page.goto('https://localhost:5173/admin/library-paths');
    await page.getByRole('button', { name: 'Customize /media/movies', exact: true }).waitFor({ timeout: 5000 }).catch(async (err) => { console.error(await page.locator('body').innerText()); throw err; });
    const open = async (path) => {
      await page.getByRole('button', { name: `Customize ${path}`, exact: true }).click();
      await page.getByRole('dialog').waitFor();
    };
    const defaults = async (movies, tv, custom) => {
      for (const [name, checked] of [['Enable for Movie Channels', movies], ['Enable for TV Show Channels', tv], ['Enable for Custom Channels', custom]]) {
        assert.equal(await page.getByRole('switch', { name, exact: true }).getAttribute('aria-checked'), String(checked));
      }
    };
    await open('/media/movies');
    await defaults(true, false, false);
    await page.getByRole('switch', { name: 'Enable for Custom Channels' }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click();
    assert.equal(writes.length, 0, 'Cancel must not write');
    await open('/media/tv');
    await defaults(false, true, false);
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    assert.equal(writes.length, 3);
    assert.deepEqual(writes.map((w) => w.body.filter.includedPaths), [['/media/movies'], ['/media/tv', '/media/movies'], ['/media/movies']]);
    writes.length = 0;
    await open('/ads');
    await defaults(true, true, false);
    await page.screenshot({ path: '/tmp/alreadyon-customize-desktop.png', fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({ path: '/tmp/alreadyon-customize-mobile.png', fullPage: true });
    const box = await page.getByRole('dialog').boundingBox();
    assert.ok(box.x >= 0 && box.x + box.width <= 391, 'Mobile dialog fits viewport');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    assert.equal(writes.length, 3);
    assert.deepEqual(writes.map((w) => w.body.folders), [['/ads'], ['/ads'], []]);
    assert.ok(writes.every((w) => w.body.explicitFolders && w.body.enabled === null));
    writes.length = 0;
    failTV = true;
    await open('/media/movies');
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('alert').waitFor();
    assert.match(await page.getByRole('alert').textContent(), /121 \(TV\)/);
    failTV = false;
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'hidden' });
    console.log('Customize browser checks passed: defaults, cancel, group overrides, commercials, failure/retry, desktop/mobile layout. All APIs mocked.');
  } finally {
    await browser.close();
  }
})().catch((err) => { console.error(err); process.exitCode = 1; });
