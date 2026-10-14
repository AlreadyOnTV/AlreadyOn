// Uses only the marked, disposable playback fixture, never a real library.
const cp = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

module.exports = async ({ browser, login, url, assert, root, seed }) => {
  assert(fs.existsSync(path.join(root, '.synthetic-test')), 'Expected isolated playback fixture');
  const mutate = sql => cp.execFileSync('python3', ['-c',
    'import sqlite3,sys\nwith sqlite3.connect(sys.argv[1]) as db:\n db.execute("PRAGMA foreign_keys=ON")\n db.executescript(sys.argv[2])',
    path.join(root, 'data/alreadyon.db'), sql]);
  seed();
  const context = await browser.newContext();
  const page = await context.newPage();
  const pageErrors = [];
  page.on('pageerror', e => pageErrors.push(e.message));
  let guest;
  const profile = async (client, id) => (await client.get(url + '/api/profiles/' + id)).json();
  const expectTune = async (targetPage, action, channel, id) => {
    // Observe both promises immediately: an action failure must not leave an
    // unhandled response waiter that masks the original error during cleanup.
    await Promise.all([
      targetPage.waitForResponse(r =>
        r.request().method() === 'POST' && r.url().endsWith(`/api/channels/${channel}/tune`) &&
        r.request().postDataJSON().profileId === id && r.status() === 201),
      action(),
    ]);
    assert((await profile(targetPage.request, id)).lastWatchedChannelId === channel,
      `Profile ${id} must persist channel ${channel} before the tune response`);
    await targetPage.waitForFunction(n => document.title === `AlreadyOn - CH ${n}`, channel);
  };
  const switchProfile = async (targetPage, name) => {
    await targetPage.getByRole('button', { name: /^Profile:/ }).click();
    await targetPage.getByRole('button', { name: 'Switch Profile', exact: true }).click();
    const row = targetPage.locator('.profile-menu-panel').getByRole('button', { name: new RegExp(`^${name}\\s`) });
    await row.locator('.profile-menu-avatar').waitFor();
    assert(await row.locator('.profile-menu-avatar').count() === 1, `Profile ${name} has its icon beside its name`);
    const spacing = await row.evaluate(element => {
      const avatar = element.querySelector('.profile-menu-avatar').getBoundingClientRect();
      const profileName = element.querySelector('.profile-menu-name').getBoundingClientRect();
      return Math.round(profileName.left - avatar.right);
    });
    assert(spacing <= 8, `Profile ${name} icon/name spacing should stay compact: ${spacing}px`);
    await row.click();
  };
  try {
    await login(page);
    const profiles = await (await context.request.get(url + '/api/profiles')).json();
    const admin = profiles.find(p => p.role === 'admin');
    const created = await context.request.post(url + '/api/profiles', {
      data: { name: 'Remembered Viewer', role: 'viewer', audienceTier: 'adult' },
    });
    assert(created.ok(), 'Create a second profile with the same audience tier');
    guest = await created.json();
    await expectTune(page, () => page.locator('.guide-row').filter({
      has: page.locator('.guide-channel-num', { hasText: /^999$/ }),
    }).locator('.guide-slot-box').first().click(), 999, admin.id);

    // Settings must restore from the database, not a module variable or a URL.
    for (const menu of ['Profile', 'Server']) {
      for (const reload of [false, true]) {
        await page.getByRole('button', { name: /^Profile:/ }).click();
        await page.getByRole('button', { name: menu, exact: true }).click();
        await page.getByRole('button', { name: /Back To Guide/ }).waitFor();
        if (reload) await page.reload();
        await expectTune(page, () => page.getByRole('button', { name: /Back To Guide/ }).click(), 999, admin.id);
      }
    }
    await expectTune(page, () => switchProfile(page, guest.name), 998, guest.id);
    await expectTune(page, () => switchProfile(page, admin.name), 999, admin.id);
    assert((await profile(context.request, guest.id)).lastWatchedChannelId === 998, 'Profiles must remain isolated');

    await page.getByRole('button', { name: /^Profile:/ }).click();
    await page.getByRole('button', { name: 'Sign Out', exact: true }).click();
    // Logout reloads the document asynchronously; finish that navigation before login starts another.
    await page.getByRole('dialog', { name: 'Sign In', exact: true }).waitFor();
    await expectTune(page, () => login(page), 999, admin.id);
    const fresh = await browser.newContext();
    try {
      const other = await fresh.newPage();
      await expectTune(other, () => login(other), 999, admin.id);
      await expectTune(other, () => switchProfile(other, guest.name), 998, guest.id);
      await other.goto(url + '/profile');
    } finally {
      await fresh.close();
    }

    // Real guide filtering and deletion, applied only to synthetic rows.
    for (const invalid of ['hidden', 'ineligible', 'empty', 'deleted']) {
      await page.goto(url + '/profile');
      mutate(`UPDATE profiles SET last_watched_channel_id=999, audience_tier='adult' WHERE id=${admin.id};
        UPDATE channels SET hidden=0, visibility='show' WHERE id=999;
        DELETE FROM channel_filters WHERE channel_id=999;`);
      if (invalid === 'hidden') mutate("UPDATE channels SET hidden=1,visibility='hide' WHERE id=999;");
      if (invalid === 'ineligible') mutate(`UPDATE profiles SET audience_tier='teen' WHERE id=${admin.id};
        INSERT INTO channel_filters(channel_id,audience_tier) VALUES(999,'adult');`);
      if (invalid === 'empty') mutate('DELETE FROM schedule_entries WHERE channel_id=999;');
      if (invalid === 'deleted') mutate('DELETE FROM channels WHERE id=999;');
      // A reload in settings also refreshes the profile's audience restriction.
      await page.reload();
      await expectTune(page, () => page.getByRole('button', { name: /Back To Guide/ }).click(), 998, admin.id);
      seed();
    }
    assert(pageErrors.length === 0, 'Browser errors: ' + JSON.stringify(pageErrors));
    console.log('PASS: per-profile immediate persistence, same-tier switches, settings/reload restoration, fresh sessions/devices, and invalid-channel fallback');
  } finally {
    await page.goto(url + '/profile').catch(() => {});
    if (guest) await context.request.delete(url + '/api/profiles/' + guest.id);
    mutate("UPDATE profiles SET audience_tier='adult' WHERE role='admin'; DELETE FROM channel_filters WHERE channel_id=999;");
    seed();
    await context.close();
  }
};
