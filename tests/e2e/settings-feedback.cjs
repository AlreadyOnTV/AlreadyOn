// UI-only fixtures: every API call is intercepted; no server account is changed.
const { chromium } = require('../../web/node_modules/playwright');
const assert = require('node:assert/strict');
const url = process.argv[2];
if (!url) throw Error('Usage: node settings-feedback.cjs <synthetic-server-url>');
(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const profile = { id: 1, name: 'Synthetic', role: 'admin', audienceTier: 'adult', hasPin: false, icon: null, guideTimeFormat: '12h', subtitleMode: 'off', audioLanguage: 'en', subtitleLanguage: 'en', lastWatchedChannelId: null };
    let pinWrites = 0;
    await page.route(url => url.pathname.startsWith('/api/'), route => {
      const request = route.request(), path = new URL(request.url()).pathname;
      if (path === '/api/playback/probe') return route.fulfill({ json: {} });
      if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false, authenticated: true, username: 'synthetic', role: 'owner' } });
      if (path === '/api/profiles') return route.fulfill({ json: [profile] });
      if (path === '/api/profiles/1') return route.fulfill({ json: profile });
      if (path === '/api/auth/tv-pin') {
        if (request.method() === 'PUT') pinWrites++;
        return route.fulfill({ json: { enabled: false } });
      }
      if (request.method() !== 'GET') { errors.push('Unexpected write: ' + path); return route.abort(); }
      if (path === '/api/users') return route.fulfill({ json: [{ id: 1, username: 'synthetic', role: 'owner', mustChangePassword: false, passwordLocked: false, maxStreams: 0 }] });
      if (path === '/api/auth/sessions') return route.fulfill({ json: [] });
      if (path === '/api/auth/session-lifetime') return route.fulfill({ json: { days: 30 } });
      return route.fulfill({ json: {} });
    });
    // Profile and account pages share the Server Settings header layout.
    for (const route of ['/profile', '/account']) {
      await page.goto(url + route);
      const back = page.locator('.admin-topbar-inner .admin-back');
      await back.waitFor();
      assert.equal(await page.locator('.admin-content .admin-back').count(), 0);
      for (const width of [1440, 768, 390]) {
        await page.setViewportSize({ width, height: 900 });
        const brandBox = await page.locator('.admin-topbar-inner .brand-link').boundingBox();
        const backBox = await back.boundingBox();
        assert.ok(width > 768 ? backBox.x >= brandBox.x + brandBox.width : backBox.y >= brandBox.y + brandBox.height);
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    for (const icon of await page.locator('.profiles-table .admin-icon-btn').all()) {
      assert.equal((await icon.boundingBox()).width, 40, 'Profile action icons must not shrink');
    }
    const helpLabel = page.locator('label[for="tv-pairing-pin"]');
    await helpLabel.hover();
    const help = await helpLabel.getAttribute('title');
    assert.ok(help.includes('used with your username'));
    assert.equal(await page.locator('#tv-pairing-pin').getAttribute('aria-description'), help);
    const icon = await helpLabel.evaluate(el => ({ mask: getComputedStyle(el, '::after').maskImage, gap: getComputedStyle(el, '::after').marginLeft }));
    assert.ok(icon.mask.includes('data:image/svg+xml'));
    assert.equal(icon.gap, '0px');
    const save = page.getByRole('button', { name: 'Save Changes', exact: true });
    await page.waitForFunction(() => document.querySelector('.account-save-row button')?.disabled === false);
    await page.clock.install();
    const feedback = page.locator('.account-save-feedback');
    await save.click();
    assert.equal(await feedback.innerText(), 'There are no changes to save.');
    assert.equal(await feedback.evaluate(el => getComputedStyle(el).animationName), 'session-saved-fade');
    assert.equal(await feedback.evaluate(el => getComputedStyle(el).animationDuration), '5s');
    const saveBox = await save.boundingBox(), feedbackBox = await feedback.boundingBox();
    assert.ok(feedbackBox.x >= saveBox.x + saveBox.width);
    await page.clock.fastForward(3000);
    await save.click();
    await page.clock.fastForward(3000);
    assert.equal(await feedback.count(), 1, 'Repeated saves restart the timer');
    await page.clock.fastForward(2100);
    assert.equal(await feedback.count(), 0);
    await page.getByLabel('TV Pairing Pin', { exact: true }).fill('123456');
    await save.click();
    await page.getByText('Changes saved.', { exact: true }).waitFor();
    assert.equal(pinWrites, 1);
    await page.clock.fastForward(5100);
    assert.equal(await feedback.count(), 0);
    await page.clock.resume();
    await page.goto(url + '/admin/accounts');
    await page.getByRole('button', { name: 'Edit', exact: true }).click();
    const form = page.locator('.account-form');
    for (const text of ['Simultaneous Streams', 'Require Password Change', 'Password Locked']) {
      const label = form.locator('label').filter({ hasText: text });
      await label.hover();
      assert.ok((await label.getAttribute('title')).length > 70);
      assert.ok((await label.evaluate(el => getComputedStyle(el, '::after').maskImage)).includes('data:image/svg+xml'));
    }
    assert.equal(await page.getByText('A locked password can only be changed by an owner,', { exact: false }).count(), 0);
    assert.equal(await form.locator('.account-form-actions').evaluate(el => getComputedStyle(el).marginTop), '24px');
    assert.deepEqual(errors, []);
    console.log('PASS: settings headers, tooltip indicators, inline five-second save feedback, Home Users help and spacing');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
