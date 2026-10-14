// Browser-only provider fixtures keep saved credentials out of the form and
// prove visibility toggles never write settings or affect another field.
module.exports = async ({ browser, login, url, assert }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await login(page);
  let writes = 0;
  await page.route('**/api/settings/metadata', route => {
    if (route.request().method() !== 'GET') writes++;
    return route.fulfill({ json: { tmdbApiKeySet: true, keylessMetadata: false } });
  });
  await page.route('**/api/settings/ddns', route => {
    if (route.request().method() !== 'GET') writes++;
    return route.fulfill({ json: { provider: 'cloudflare', running: '', configs: [{ provider: 'cloudflare', hostname: 'demo.example.com', username: '', hasSecret: true, enabled: true }] } });
  });
  await page.goto(url + '/admin/server');
  const key = page.getByRole('textbox', { name: 'TMDb API Key', exact: true }).or(page.getByLabel('TMDb API Key', { exact: true }));
  const credential = page.getByLabel('DDNS credential', { exact: true });
  await key.waitFor(); await credential.waitFor();
  const toggle = field => field.locator('..').locator('button');
  for (const field of [key, credential]) {
    assert(await field.inputValue() === '', 'Saved credential is never loaded into the input');
    assert(await toggle(field).count() === 0, 'No eye control for an untouched saved credential');
    await field.fill('synthetic-draft');
    assert(await field.getAttribute('type') === 'password', 'Replacement starts masked');
    await toggle(field).click();
    assert(await field.getAttribute('type') === 'text' && await field.inputValue() === 'synthetic-draft', 'Pointer reveals only the typed draft');
    await toggle(field).focus(); await toggle(field).press('Enter');
    assert(await field.getAttribute('type') === 'password', 'Keyboard masks the draft');
    await toggle(field).click();
    await field.fill('');
    assert(await toggle(field).count() === 0, 'Clearing removes the eye control');
    await field.fill('second-draft');
    assert(await field.getAttribute('type') === 'password', 'A new draft is masked after clearing a visible draft');
    await field.fill('');
  }
  await key.fill('unsaved-key'); await toggle(key).click();
  assert(await toggle(credential).count() === 0, 'The other saved field stays unrevealable');
  await page.reload();
  await key.waitFor(); await credential.waitFor();
  assert(await key.inputValue() === '' && await credential.inputValue() === '', 'Reload discards unsaved drafts');
  assert(await toggle(key).count() === 0 && await toggle(credential).count() === 0, 'Reload hides both controls');
  assert(writes === 0, 'Visibility and draft editing never save credentials');
  await context.close();
  console.log('PASS: saved credential placeholders, draft-only visibility, clearing/reload and zero writes');
};
