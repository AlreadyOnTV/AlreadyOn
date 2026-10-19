// Focused WebUI test: intercept every API call with an in-memory profile.
// The preview only serves the app; no account, PIN or preference is changed there.
const { chromium } = require('../../web/node_modules/playwright');
const assert = require('node:assert/strict');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
const until = async predicate => {
  for (let i = 0; i < 200; i++) { if (predicate()) return; await wait(25); }
  throw new Error('Timed out waiting for mock request');
};

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const profile = {
      id: 1, name: 'Synthetic profile', role: 'admin', audienceTier: 'adult', hasPin: false,
      icon: null, guideTimeFormat: '12h', subtitleMode: 'off', audioLanguage: 'en', subtitleLanguage: 'en',
      fontLevel: null, brightnessLevel: null, subtitlesEnabled: false, lastWatchedChannelId: null,
    };
    const writes = [];
    let iconImage;
    await page.route('**/profile-icons/synthetic.png', route => route.fulfill({ contentType: 'image/png', body: iconImage }));
    let failNext = false;
    let holdNext = false;
    let release;
    await page.route(url => url.pathname.startsWith('/api/'), async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      const method = request.method();
      if (path === '/api/playback/probe') return route.fulfill({ json: {} });
      if (path === '/api/auth/status') return route.fulfill({ json: { setupRequired: false, authenticated: true, username: 'synthetic', role: 'owner' } });
      if (path === '/api/profiles' && method === 'GET') return route.fulfill({ json: [{ ...profile }] });
      if (path === '/api/profiles/1' && method === 'GET') return route.fulfill({ json: { ...profile } });
      if (path === '/api/profiles/1' && method === 'PATCH') {
        const patch = request.postDataJSON();
        writes.push({ method, path, patch });
        if (holdNext) { holdNext = false; await new Promise(resolve => { release = resolve; }); }
        if (failNext) { failNext = false; return route.fulfill({ status: 500, json: { error: 'Synthetic save failure' } }); }
        Object.assign(profile, patch);
        return route.fulfill({ json: { ...profile } });
      }
      if (path === '/api/profiles/1/icon' && method === 'POST') {
        writes.push({ method, path });
        profile.icon = '/profile-icons/synthetic.png';
        return route.fulfill({ json: { ...profile } });
      }
      if (path === '/api/profiles/1/pin' && ['PUT', 'DELETE'].includes(method)) {
        writes.push({ method, path });
        profile.hasPin = method === 'PUT';
        return route.fulfill({ json: {} });
      }
      if (method !== 'GET') {
        errors.push(`Unexpected write: ${method} ${path}`);
        return route.abort();
      }
      return route.fulfill({ json: {} });
    });
    const url = process.argv[2] || 'https://localhost:5173';
    await page.goto(url + '/profile');
    const saved = key => page.getByRole('status', { name: `${key} saved`, exact: true });
    const name = page.getByLabel('Name', { exact: true });
    await name.waitFor();
    await page.waitForTimeout(800);
    assert.equal(writes.length, 0, 'No writes on mount');
    assert.equal(await page.getByRole('button', { name: /^(Save.*|Cancel)$/ }).count(), 0);
    const time = page.getByLabel('Time Format', { exact: true });
    await time.selectOption('12h');
    await page.waitForTimeout(100);
    assert.equal(writes.length, 0, 'Unchanged selection is not saved');
    await time.selectOption('24h');
    await saved('guideTimeFormat').waitFor();
    assert.deepEqual(writes[0].patch, { guideTimeFormat: '24h' });
    await page.waitForTimeout(4800);
    assert.equal(await saved('guideTimeFormat').evaluate(el => getComputedStyle(el).opacity), '1', 'Check remains green for five seconds');
    await saved('guideTimeFormat').waitFor({ state: 'detached' });
    await time.selectOption('24h');
    await page.waitForTimeout(100);
    assert.equal(writes.length, 1, 'Unchanged values do not reset feedback or write again');
    await page.reload();
    await time.waitFor();
    assert.equal(await time.inputValue(), '24h', 'Saved value survives reload');
    await page.getByLabel('Preferred Audio Language').selectOption('de');
    await saved('audioLanguage').waitFor();
    await page.getByLabel('Subtitle language', { exact: true }).selectOption('de');
    await saved('subtitleLanguage').waitFor();
    await page.getByLabel('Subtitles', { exact: true }).selectOption('always');
    await saved('subtitleMode').waitFor();
    await name.fill('   ');
    await page.waitForTimeout(800);
    assert.equal(writes.length, 4, 'Invalid name is not saved');
    await name.fill('  New synthetic name  ');
    await saved('name').waitFor();
    await name.blur();
    await page.waitForTimeout(800);
    assert.equal(writes.length, 5, 'Debounce and blur share one save');
    assert.equal(profile.name, 'New synthetic name');
    await page.getByRole('button', { name: 'Set PIN', exact: true }).click();
    await page.getByLabel('New PIN', { exact: true }).fill('123456');
    await page.getByLabel('Confirm PIN', { exact: true }).fill('12345');
    await page.waitForTimeout(800);
    assert.equal(writes.length, 5, 'Mismatched PIN is not saved');
    await page.getByLabel('Confirm PIN', { exact: true }).fill('123456');
    await page.waitForTimeout(800);
    assert.equal(writes.length, 5, 'Even a matching PIN waits for Save PIN');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Set PIN', exact: true }).waitFor();
    assert.equal(writes.length, 5, 'Cancel leaves the PIN unchanged');
    await page.getByRole('button', { name: 'Set PIN', exact: true }).click();
    assert.equal(await page.getByLabel('New PIN', { exact: true }).inputValue(), '');
    await page.getByLabel('New PIN', { exact: true }).fill('123456');
    await page.getByLabel('Confirm PIN', { exact: true }).fill('123456');
    await page.getByRole('button', { name: 'Save PIN', exact: true }).click();
    await saved('pin').waitFor();
    await page.getByRole('button', { name: 'Change PIN', exact: true }).waitFor();
    await page.waitForTimeout(800);
    assert.equal(writes.length, 6, 'Matching PIN saves exactly once');
    await page.getByRole('button', { name: 'Remove PIN', exact: true }).click();
    assert.equal(writes.length, 6, 'PIN removal still requires confirmation');
    await page.getByRole('button', { name: 'Confirm remove?', exact: true }).click();
    await page.getByRole('button', { name: 'Set PIN', exact: true }).waitFor();
    assert.equal(writes.length, 7);
    const subtitle = page.getByLabel('Subtitle language', { exact: true });
    holdNext = true;
    await subtitle.selectOption('fr');
    await until(() => !!release);
    await subtitle.selectOption('it');
    await subtitle.selectOption('es');
    release();
    await until(() => profile.subtitleLanguage === 'es');
    await saved('subtitleLanguage').waitFor();
    assert.deepEqual(writes.slice(-2).map(write => write.patch), [{ subtitleLanguage: 'fr' }, { subtitleLanguage: 'es' }], 'Queued intermediate edits are coalesced and newest wins');
    failNext = true;
    await page.getByLabel('Preferred Audio Language').selectOption('fr');
    await page.getByRole('alert').waitFor();
    await page.waitForTimeout(800);
    assert.equal(profile.audioLanguage, 'de', 'Failed write does not change confirmed value');
    assert.equal(await saved('audioLanguage').count(), 0, 'Failed save never shows a green check');
    const failedCount = writes.length;
    await page.waitForTimeout(800);
    assert.equal(writes.length, failedCount, 'No automatic retry loop');
    await page.getByLabel('Preferred Audio Language').selectOption('de');
    await page.getByRole('alert').waitFor({ state: 'detached' });
    assert.equal(writes.length, failedCount, 'Returning to the saved value clears an error without a write');
    await page.getByLabel('Preferred Audio Language').selectOption('fr');
    await saved('audioLanguage').waitFor();
    assert.equal(profile.audioLanguage, 'fr', 'A failed change can be retried');
    const beforeIcon = writes.length;
    const preset = page.locator('.profile-icon-option').first();
    await preset.click();
    await until(() => writes.length === beforeIcon + 1);
    await saved('icon').waitFor();
    await preset.click();
    await page.waitForTimeout(100);
    assert.equal(writes.length, beforeIcon + 1, 'Unchanged preset is not saved');
    iconImage = Buffer.from(await page.evaluate(() => {
      const canvas = document.createElement('canvas');
      canvas.width = canvas.height = 8;
      canvas.getContext('2d').fillRect(0, 0, 8, 8);
      return canvas.toDataURL('image/png').split(',')[1];
    }), 'base64');
    const upload = { name: 'synthetic.png', mimeType: 'image/png', buffer: iconImage };
    await page.locator('input[type=file]').setInputFiles(upload);
    await until(() => profile.icon === '/profile-icons/synthetic.png');
    await saved('icon').waitFor();
    const afterUpload = writes.length;
    await page.locator('input[type=file]').setInputFiles(upload);
    await page.waitForTimeout(800);
    assert.equal(writes.length, afterUpload, 'Identical uploaded image is not uploaded again');
    profile.audioLanguage = 'it';
    await page.evaluate(() => window.dispatchEvent(new Event('focus')));
    await page.waitForFunction(() => document.querySelector('#preferred-audio-language').value === 'it');
    await page.getByLabel('Preferred Audio Language').selectOption('it');
    await page.waitForTimeout(100);
    assert.equal(writes.length, afterUpload, 'Remote refresh rebases unchanged settings without a write');
    for (const width of [390, 1280]) {
      await page.setViewportSize({ width, height: 900 });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'No horizontal overflow');
      await page.screenshot({ path: `/tmp/alreadyon-profile-autosave-${width}.png`, fullPage: true });
    }
    assert.deepEqual(errors, []);
    console.log('PASS: profile autosave, five-second confirmation/fade, unchanged values, PIN, queued edits, failure/retry, and responsive layout');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
