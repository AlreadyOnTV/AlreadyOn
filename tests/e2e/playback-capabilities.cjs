// Only static preview modules are fetched; every management request is intercepted.
const { chromium, webkit } = require('../../web/node_modules/playwright');
const assert = require('node:assert/strict');

(async () => {
  const browserName = process.argv[3] || 'chromium';
  assert(['chromium', 'webkit'].includes(browserName), 'Select chromium or webkit');
  const browser = await ({ chromium, webkit }[browserName]).launch({ headless: true });
  const origin = process.argv[2] || 'https://localhost:5173';
  try {
    for (const scenario of ['actual', 'managed-only', 'prefer-managed', 'prefixed-only', 'native-only', 'unsupported', 'standard']) {
      const context = await browser.newContext({ ignoreHTTPSErrors: true });
      try {
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.route('**/__capability_test', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>Playback capability regression</title>' }));
        await page.route(url => url.pathname.startsWith('/api/'), route => route.fulfill({ status: 503, json: { error: 'Synthetic probe unavailable' } }));
        await page.goto(`${origin}/__capability_test`);
        const result = await page.evaluate(async scenario => {
          const probes = [];
          if (scenario !== 'actual') {
            const supported = scenario !== 'unsupported';
            HTMLMediaElement.prototype.canPlayType = function () { return supported ? 'probably' : ''; };
            Object.defineProperty(navigator, 'mediaCapabilities', { configurable: true, value: { decodingInfo: async configuration => { probes.push(configuration.type); return { supported, smooth: supported, powerEfficient: supported }; } } });
            const source = { isTypeSupported: () => supported };
            // Emulate iPhone's API availability, not its OS media decoders.
            for (const key of ['MediaSource', 'ManagedMediaSource', 'WebKitMediaSource']) Object.defineProperty(window, key, { configurable: true, writable: true, value: undefined });
            if (scenario === 'managed-only' || scenario === 'prefer-managed') window.ManagedMediaSource = source;
            if (scenario === 'prefer-managed') window.MediaSource = { isTypeSupported: () => false };
            if (scenario === 'prefixed-only') window.WebKitMediaSource = source;
            if (scenario === 'standard') window.MediaSource = source;
          }
          const { clientCapabilities } = await import('/src/playback/capabilities.ts');
          return { caps: await clientCapabilities(), probes };
        }, scenario);
        assert.deepEqual(errors, [], scenario);
        if (scenario !== 'actual') {
          assert.equal(result.caps.hls, scenario !== 'unsupported', scenario);
          assert.equal(result.caps.video.length > 0, scenario !== 'unsupported', scenario);
          assert.equal(result.caps.audio.length > 0, scenario !== 'unsupported', scenario);
          const mse = !['native-only', 'unsupported'].includes(scenario);
          assert.equal(result.probes.includes('media-source'), mse, scenario);
          assert.equal(result.caps.audio.some(a => a.hls), mse, scenario);
        }
        console.log(`${browserName}: ${scenario} passed (HLS=${result.caps.hls})`);
      } finally { await context.close(); }
    }
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
