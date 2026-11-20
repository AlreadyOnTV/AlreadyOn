// Run only inside playback.sh's marked disposable server. Persistent Chromium
// profiles exercise actual on-disk browser storage, not a rehydrated JSON mock.
const { chromium } = require('../../web/node_modules/playwright');
const path = require('node:path');
const fs = require('node:fs');

module.exports = async ({ login, playing, seed, url, assert, leave }) => {
  const root = process.env.PLAYBACK_TEST_ROOT;
  if (!root || !fs.existsSync(path.join(root, '.synthetic-test'))) throw Error('Run playback.sh');
  let context;
  const errors = [];
  const open = async (device) => {
    context = await chromium.launchPersistentContext(path.join(root, 'volume-' + device), {
      headless: true,
      args: ['--autoplay-policy=document-user-activation-required'],
    });
    await context.addInitScript(() => {
      window.__volumeWrites = [];
      const setItem = Storage.prototype.setItem;
      Storage.prototype.setItem = function (key, value) {
        if (key.startsWith('alreadyon.volume.')) window.__volumeWrites.push([key, value]);
        return setItem.call(this, key, value);
      };
      const play = HTMLMediaElement.prototype.play;
      HTMLMediaElement.prototype.play = function () {
        if (window.__blockNextUnmutedPlay && !this.muted) {
          window.__blockNextUnmutedPlay = false;
          return Promise.reject(new DOMException('Synthetic autoplay block', 'NotAllowedError'));
        }
        return play.call(this);
      };
    });
    const page = context.pages()[0];
    page.on('pageerror', error => errors.push(error.message));
    seed();
    await login(page);
    await page.locator('.guide-slot-box').first().click();
    await playing(page);
    return page;
  };
  const level = (page, volume, muted) => page.waitForFunction(({ volume, muted }) => {
    const video = document.querySelector('video');
    return video && Math.abs(video.volume - volume) < 0.001 && video.muted === muted;
  }, { volume, muted });
  const writes = page => page.evaluate(() => window.__volumeWrites.length);
  const stored = (page, id) => page.evaluate(id => localStorage.getItem('alreadyon.volume.' + id), id);
  const volume = async (page, value) => {
    await page.locator('.playback-player').hover();
    const slider = page.getByRole('slider', { name: 'Volume', exact: true });
    const box = await slider.boundingBox();
    const x = fraction => box.x + 8 + (box.width - 16) * fraction;
    await page.mouse.move(x(Number(await slider.inputValue())), box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(x(value), box.y + box.height / 2, { steps: 8 });
    await page.mouse.up();
    assert(Math.abs(Number(await slider.inputValue()) - value) < 0.001,
      `Dragged slider to ${await slider.inputValue()}, expected ${value}`);
    await level(page, value, value === 0);
  };
  const mute = async page => {
    await page.locator('.playback-player').hover();
    await page.getByRole('button', { name: /^(Mute|Unmute)$/, exact: true }).click();
  };
  const switchProfile = async (page, name) => {
    await page.locator('.profile-menu-trigger').click();
    await page.getByRole('button', { name: 'Switch Profile', exact: true }).click();
    await page.locator('.profile-menu-profile').filter({ hasText: name }).click();
    await page.locator('.profile-menu-trigger').filter({ hasText: name }).waitFor();
    await page.locator('.guide-slot-box').first().click();
    await playing(page);
  };
  try {
    let page = await open('one');
    const profiles = await (await page.request.get(url + '/api/profiles')).json();
    const selectedId = await page.evaluate(() => Number(localStorage.getItem('alreadyon.currentUserId')));
    const firstId = profiles.find(profile => profile.id === selectedId)?.id ?? profiles.find(profile => profile.role === 'admin')?.id ?? profiles[0].id;
    const firstName = profiles.find(profile => profile.id === firstId).name;
    const created = await page.request.post(url + '/api/profiles', {
      data: { name: 'Volume synthetic adult', role: 'viewer', audienceTier: 'adult' },
    });
    assert(created.ok(), 'Create isolated volume-test profile');
    const second = await created.json();
    await page.reload();
    await playing(page);
    await level(page, 1, true);
    assert(await writes(page) === 0, 'Startup never saves a volume');
    await volume(page, 0.5);
    assert(await stored(page, firstId) === '0.5', 'Manual slider persists 50%');
    let count = await writes(page);
    await mute(page);
    await level(page, 0.5, true);
    await mute(page);
    await level(page, 0.5, false);
    await page.keyboard.press('m');
    await level(page, 0.5, true);
    await page.keyboard.press('m');
    await level(page, 0.5, false);
    assert(await writes(page) === count, 'Mute button and m never overwrite saved volume');

    await page.locator('.guide-row').nth(1).locator('.guide-slot-box').first().click();
    await playing(page);
    await level(page, 0.5, false);
    assert(await writes(page) === count, 'Channel changes retain volume without saving');

    // Volume events caused by media/native code are not a manual slider adjustment.
    await page.evaluate(() => { document.querySelector('video').volume = 0.9; });
    await mute(page);
    await level(page, 0.5, true);
    assert(await stored(page, firstId) === '0.5' && await writes(page) === count,
      'Programmatic volumechange and mute cannot replace manual volume');
    await page.locator('.playback-player').hover();
    await page.getByRole('button', { name: 'Full Screen', exact: true }).click();
    await page.waitForFunction(() => !!document.fullscreenElement);
    await level(page, 0.5, false);
    await page.keyboard.press('f');
    await page.waitForFunction(() => !document.fullscreenElement);
    assert(await writes(page) === count, 'Fullscreen entry restores volume without saving');

    console.log('PASS: volume slider, mute/m/fullscreen, channel changes and programmatic volume events');
    // Exercise the real player's NotAllowedError fallback after a channel retune.
    await page.evaluate(() => { window.__blockNextUnmutedPlay = true; });
    await page.locator('.guide-row').first().locator('.guide-slot-box').first().click();
    await playing(page);
    await level(page, 0.5, true);
    assert(await stored(page, firstId) === '0.5' && await writes(page) === count,
      'Autoplay fallback does not overwrite saved volume');

    await switchProfile(page, second.name);
    await level(page, 1, true);
    await volume(page, 0.25);
    count = await writes(page);
    await switchProfile(page, firstName);
    await level(page, 0.5, true);
    await mute(page);
    await level(page, 0.5, false);
    await switchProfile(page, second.name);
    await level(page, 0.25, true);
    assert(await writes(page) === count, 'Profile switching never writes another preference');
    await switchProfile(page, firstName);

    await page.reload();
    await playing(page);
    await level(page, 0.5, true);
    assert(await writes(page) === 0 && await stored(page, second.id) === '0.25',
      'Reload keeps profile preferences through legacy cleanup and starts muted');
    await page.keyboard.press('m');
    await level(page, 0.5, false);
    await leave(page);
    await context.close();
    context = null;

    page = await open('one');
    await level(page, 0.5, true);
    assert(await writes(page) === 0, 'Full browser restart preserves volume without saving');
    await mute(page);
    await level(page, 0.5, false);
    await volume(page, 0);
    assert(await stored(page, firstId) === '0', 'Explicit manual zero is remembered');
    count = await writes(page);
    await mute(page);
    await level(page, 0, false);
    await page.keyboard.press('f');
    await level(page, 0, false);
    await page.keyboard.press('f');
    assert(await writes(page) === count && await stored(page, firstId) === '0',
      'Unmute/fullscreen respect explicit zero without inventing a new level');
    await volume(page, 0.5);
    await leave(page);
    await context.close();
    context = null;

    console.log('PASS: volume profiles, autoplay fallback, reload, browser restart and manual zero');
    page = await open('two');
    await level(page, 1, true);
    assert(await stored(page, firstId) === null, 'A second device starts with its own default');
    await volume(page, 0.8);
    await leave(page);
    await context.close();
    context = null;
    page = await open('one');
    await level(page, 0.5, true);
    assert(await stored(page, firstId) === '0.5', 'Second device does not change first-device volume');
    const otherAccount = { username: 'volume-other-account', password: 'volume-password-123' };
    const accountResponse = await page.request.post(url + '/api/users', {
      data: { ...otherAccount, role: 'viewer', mustChangePassword: false, passwordLocked: false, maxStreams: 0 },
    });
    assert(accountResponse.ok(), 'Create isolated second account');
    await leave(page);
    const signedIn = await page.request.post(url + '/api/auth/login', { data: otherAccount });
    assert(signedIn.ok(), 'Sign into isolated second account in the same browser');
    await page.goto(url);
    await page.locator('.guide-slot-box').first().click();
    await playing(page);
    const otherProfiles = await (await page.request.get(url + '/api/profiles')).json();
    const otherId = (otherProfiles.find(profile => profile.role === 'admin') ?? otherProfiles[0]).id;
    assert(otherId !== firstId, 'Different accounts have distinct profile IDs');
    await level(page, 1, true);
    await volume(page, 0.2);
    assert(await stored(page, firstId) === '0.5' && await stored(page, otherId) === '0.2',
      'Accounts on one device retain independent saved volume');
    await page.reload();
    await playing(page);
    await level(page, 0.2, true);
    await page.keyboard.press('m');
    await level(page, 0.2, false);
    assert(await writes(page) === 0, 'New-account reload and unmute never save volume');
    assert(errors.length === 0, 'No browser errors: ' + errors.join('; '));
    console.log('PASS: WebUI volume: real slider, mute/m/fullscreen, channel changes, autoplay fallback, profiles/accounts, reload, browser restart, manual zero and independent devices');
  } finally {
    if (context) {
      const page = context.pages()[0];
      if (page && !page.isClosed()) await page.goto(url + '/profile').catch(() => {});
      await context.close();
    }
  }
};
