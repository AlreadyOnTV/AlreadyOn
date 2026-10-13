// Isolated folder picker integration: no development database or library is used.
// Run: node tests/e2e/folders.cjs (after npm --prefix web run build).
const { chromium } = require('../../web/node_modules/playwright');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const cp = require('node:child_process');
const assert = require('node:assert/strict');
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'alreadyon-folders-'));
const repo = path.resolve(__dirname, '../..');
const ports = cp.execFileSync('python3', ['-c', 'import socket\na=socket.socket(); b=socket.socket(); a.bind(("127.0.0.1",0)); b.bind(("127.0.0.1",0)); print(a.getsockname()[1], b.getsockname()[1])'], { encoding: 'utf8' }).trim().split(' ');
const url = `http://localhost:${ports[0]}`;
let server, browser;
(async () => {
  for (const name of ['TV Shows', 'Movies', 'Commercials']) fs.mkdirSync(path.join(root, 'media', name), { recursive: true });
  cp.execFileSync('go', ['build', '-tags=e2e', '-ldflags', `-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir=${root}/data -X github.com/alreadyonapp/alreadyon/server/internal/config.webDir=${repo}/web/dist`, '-o', `${root}/server`, './cmd/server'], { cwd: `${repo}/server` });
  server = cp.spawn(`${root}/server`, [], { env: { ...process.env, ALREADYON_WEB_PORT: ports[0] }, stdio: 'ignore' });
  for (let i = 0; i < 100; i++) { try { if ((await fetch(url + '/healthz')).ok) break; } catch {} await new Promise(r => setTimeout(r, 100)); }
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const setup = await context.request.post(url + '/api/auth/setup', { data: { username: 'folder-test', password: require('node:crypto').randomBytes(18).toString('hex') } });
  assert.equal(setup.ok(), true);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  for (const [tab, title, name, key] of [['media', 'TV Show Folders', 'TV Shows', 'tv'], ['media', 'Movie Folders', 'Movies', 'movies'], ['commercials', 'Commercial Folders', 'Commercials', 'commercials']]) {
    await page.goto(url + '/admin/' + tab);
    const card = page.locator('.admin-card').filter({ has: page.getByRole('heading', { name: title, exact: true }) });
    const add = card.getByRole('button', { name: '+ Add folder', exact: true });
    await add.click();
    const modal = page.getByRole('dialog', { name: 'Add folder — ' + title });
    await modal.waitFor();
    assert.equal(await modal.getByRole('textbox', { name: 'Folder path' }).inputValue(), '/data/media/');
    // Escape cancels and native dialog restores focus to its opener.
    await page.keyboard.press('Escape');
    await modal.waitFor({ state: 'detached' });
    assert.equal(await add.evaluate(el => el === document.activeElement), true);
    await add.click();
    await modal.getByRole('textbox', { name: 'Folder path' }).fill(root + '/missing');
    await modal.getByRole('button', { name: 'Go', exact: true }).click();
    await modal.getByRole('alert').waitFor();
    assert.equal(await modal.getByRole('button', { name: 'Select Folder' }).isDisabled(), true);
    await modal.getByRole('textbox', { name: 'Folder path' }).fill(root + '/media');
    await modal.getByRole('button', { name: 'Go', exact: true }).click();
    await modal.getByRole('button', { name: new RegExp(name) }).click();
    await modal.getByText('No subfolders. You can select this folder.').waitFor();
    await modal.getByRole('button', { name: '↑ Up', exact: true }).click();
    await modal.getByRole('button', { name: new RegExp(name) }).click();
    await modal.getByText('No subfolders. You can select this folder.').waitFor();
    await modal.getByRole('button', { name: 'Select Folder' }).click();
    await modal.waitFor({ state: 'detached' });
    await card.getByText(root + '/media/' + name, { exact: true }).waitFor();
    if (tab === 'commercials') await page.getByRole('button', { name: /Save/ }).click();
    await page.waitForFunction(async ({ key, expected }) => {
      const settings = await (await fetch('/api/settings/folders')).json();
      return settings[key].paths.some(p => p.path === expected);
    }, { key, expected: root + '/media/' + name });
    console.log(`${title}: default path, cancel/focus, errors, navigation, selection and save passed`);
  }
  assert.deepEqual(errors, []);
})().catch(err => { console.error(err); process.exitCode = 1; }).finally(async () => {
  if (browser) await browser.close();
  if (server) { server.kill('SIGTERM'); await new Promise(resolve => server.once('exit', resolve)); }
  fs.rmSync(root, { recursive: true, force: true });
});
