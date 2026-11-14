// Verify the channel editor's commercial controls: persistence, folder
// selection, removed-folder fallback and returning to the global defaults.
module.exports = async ({ browser, login, url, assert }) => {
  const context = await browser.newContext(); const page = await context.newPage();
  await login(page, "/admin/channels", ".admin-card");
  const savedFolders = await (await page.request.get(url + '/api/settings/folders')).json();
  const old = savedFolders.commercials.source === 'default' ? null : savedFolders.commercials.paths.map(p => p.path);
  const name = 'Channel Commercial Controls';
  const channel = await (await page.request.post(url + '/api/channels', { data: { number: 9901, name } })).json();
  const toggle = (label) => page.getByRole('dialog').getByRole('switch', { name: label, exact: true });
  const open = async () => {
    await page.goto(url + '/admin/channels');
    await page.getByRole('button', { name: `Edit ${name}`, exact: true }).click();
    await toggle('Enable Commercials').waitFor();
  };
  const save = async () => {
    await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
    await page.getByRole('dialog').waitFor({ state: 'detached' });
  };
  const policy = async () => (await page.request.get(url + `/api/channels/${channel.id}/commercials`)).json();
  try {
    assert((await page.request.put(url + '/api/settings/folders', { data: { commercials: ['/synthetic-ads/one', '/synthetic-ads/two'] } })).ok(), 'Set disposable commercial paths');
    await open();
    assert(await toggle('/synthetic-ads/one').getAttribute('aria-checked') === 'true', 'Every path starts selected');
    await toggle('/synthetic-ads/one').click();
    await save();
    let p = await policy();
    assert(p.folders.length === 1 && p.folders[0] === '/synthetic-ads/two', 'Saved folder selection');
    await open();
    assert(await toggle('/synthetic-ads/one').getAttribute('aria-checked') === 'false', 'Selection survives reload');
    await toggle('Enable Commercials').click();
    await save();
    p = await policy();
    assert(p.effectiveEnabled === false, 'Commercials turned off for the channel');
    await page.request.put(url + '/api/settings/folders', { data: { commercials: ['/synthetic-ads/one'] } });
    p = await policy();
    assert(p.foldersFallback, 'Removed folder falls back to all');
    await open();
    await toggle('Enable Commercials').click();
    if (!p.globalEnabled) await toggle('Enable Commercials').click();
    await save();
    p = await policy();
    assert(p.folders.length === 0 && !p.foldersFallback && p.effectiveEnabled === p.globalEnabled, 'Global defaults restored');
    console.log('PASS: channel editor commercials save/reload, folder selection, removed-folder fallback and defaults');
  } finally {
    await page.request.delete(url + `/api/channels/${channel.id}`);
    await page.request.put(url + '/api/settings/folders', { data: { commercials: old } });
    await context.close();
  }
};
