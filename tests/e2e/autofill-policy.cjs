// Verify manager hints and script-event rejection on real routed UI without
// writing settings; real input remains supported and sign-in is exempt.
module.exports = async ({ browser, login, url, assert }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  // The isolated harness may begin with first-run setup. Bootstrap that account
  // before checking the separate, actual sign-in form's manager exception.
  await login(page);
  await context.clearCookies();
  await page.goto(url);
  const signIn = page.locator('[data-password-manager-login="true"]');
  await signIn.waitFor();
  assert(await signIn.locator('[name="username"]').getAttribute('autocomplete') === 'username', 'Sign-in username keeps autofill');
  assert(await signIn.locator('[name="password"]').getAttribute('autocomplete') === 'current-password', 'Sign-in password keeps autofill');
  assert(await signIn.locator('[data-bwignore]').count() === 0, 'Sign-in is exempt from manager hints');
  await login(page);
  for (const route of ['/profile', '/account', '/admin/server', '/admin/accounts', '/admin/media', '/admin/channels', '/admin/api']) {
    await page.goto(url + route);
    await page.locator('.admin-content').waitFor();
    await page.waitForFunction(() => [...document.querySelectorAll('input,textarea,select,form')].every(e => e.getAttribute('autocomplete') === 'off' && e.getAttribute('data-bwignore') === 'true'));
  }
  await page.goto(url + '/admin/server');
  const field = page.getByLabel('TMDb API Key', { exact: true });
  await field.waitFor();
  await field.fill('user-entered-draft');
  await field.evaluate(e => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(e, 'injected-manager-value');
    e.dispatchEvent(new Event('input', { bubbles: true }));
    e.dispatchEvent(new Event('change', { bubbles: true }));
  });
  assert(await field.inputValue() === 'user-entered-draft', 'Script autofill cannot replace the accepted draft');
  await field.press('End'); await field.press('x');
  assert(await field.inputValue() === 'user-entered-draftx', 'Trusted keyboard input still updates the draft');
  await field.fill('');
  await page.evaluate(() => {
    const dialog = document.createElement('div'); dialog.id = 'autofill-policy-fixture';
    dialog.innerHTML = '<form><input value="fixture-original"><textarea>original</textarea></form>';
    document.body.append(dialog);
  });
  await page.waitForFunction(() => document.querySelector('#autofill-policy-fixture input').getAttribute('data-bwignore') === 'true');
  const dynamic = page.locator('#autofill-policy-fixture input');
  await dynamic.evaluate(e => { e.value = 'injected'; e.dispatchEvent(new Event('input', { bubbles: true })); });
  assert(await dynamic.inputValue() === 'fixture-original', 'Dynamic fields receive the same guard');
  await page.evaluate(() => document.querySelector('#autofill-policy-fixture').remove());
  await context.close();
  console.log('PASS: app-wide autofill hints, login exemption, dynamic fields, script rejection and trusted typing');
};
