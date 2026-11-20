// Called before the harness waits for calibration, against its disposable server.
module.exports = async ({ browser, login, playing, seed, url, assert }) => {
  seed();
  const context = await browser.newContext();
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  let denied = 0;
  page.on('response', async response => {
    if (response.request().method() === 'POST' && response.url().includes('/api/playback/sessions') && response.status() === 503) denied++;
  });
  // Keep this short synthetic airing current while real startup benchmarks run.
  const reseed = setInterval(seed, 30000);
  try {
    await login(page);
    await page.locator('.guide-slot-box').first().click();
    const message = page.locator('.playback-message');
    await message.filter({ hasText: 'Server starting, please wait.' }).waitFor();
    assert(await message.getAttribute('role') === 'status', 'Startup waiting is not a playback error');
    assert((await page.request.get(url + '/healthz')).ok(), 'Website is healthy during startup benchmarks');
    await page.waitForFunction(() => document.querySelector('.playback-message')?.textContent === 'Server starting, please wait.');
    // No reload or second channel click: the player must retry automatically.
    await page.waitForFunction(() => {
      const video = document.querySelector('video');
      return video?.currentTime > 1 && !video.paused && video.readyState >= 2;
    }, {}, { timeout: 2100000 });
    clearInterval(reseed);
    await playing(page);
    assert(denied > 0, 'Real server rejected playback before startup readiness');
    const auth = await (await page.request.get(url + '/api/auth/status')).json();
    assert(auth.authenticated, 'Startup waiting preserves authentication');
    assert(errors.length === 0, 'No browser errors: ' + errors.join('; '));
    console.log('PASS: real startup benchmarks block playback, WebUI stays healthy, waiting message appears and playback starts automatically afterward');
  } finally {
    clearInterval(reseed);
    await page.goto(url + '/profile').catch(() => {});
    await context.close();
  }
};
