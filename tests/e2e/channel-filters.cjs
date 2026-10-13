const { chromium } = require('../../web/node_modules/playwright');
const fs = require('node:fs'),
  os = require('node:os'),
  path = require('node:path'),
  cp = require('node:child_process'),
  assert = require('node:assert/strict');
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'alreadyon-filters-'));
const repo = path.resolve(__dirname, '../..');
const ports = cp
  .execFileSync(
    'python3',
    ['-c', 'import socket\na=socket.socket();b=socket.socket();a.bind(("127.0.0.1",0));b.bind(("127.0.0.1",0));print(a.getsockname()[1],b.getsockname()[1])'],
    { encoding: 'utf8' },
  )
  .trim()
  .split(' ');
const url = 'http://localhost:' + ports[0];
let server, browser;
(async () => {
  cp.execFileSync(
    'go',
    [
      'build',
      '-tags=e2e',
      '-ldflags',
      '-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir=' +
        root +
        '/data -X github.com/alreadyonapp/alreadyon/server/internal/config.webDir=' +
        repo +
        '/web/dist',
      '-o',
      root + '/server',
      './cmd/server',
    ],
    { cwd: repo + '/server' },
  );
  server = cp.spawn(root + '/server', [], {
    env: {
      ...process.env,
      ALREADYON_WEB_PORT: ports[0],
    },
    stdio: 'ignore',
  });
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(url + '/healthz')).ok) break;
    } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ hasTouch: true });
  assert.equal(
    (
      await context.request.post(url + '/api/auth/setup', {
        data: {
          username: 'filter-test',
          password: require('node:crypto').randomBytes(18).toString('hex'),
        },
      })
    ).ok(),
    true,
  );
  cp.execFileSync('python3', [
    '-c',
    [
      'import sqlite3,sys',
      'db=sqlite3.connect(sys.argv[1])',
      'for i,title in enumerate(["Holiday Film","Wild West Story","Other Film"],1):',
      ' db.execute("INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES(?,?,?,?,1,1,1,7200,?)",(i,"media","/synthetic/movies/"+str(i)+".mkv",1,"ok"))',
      ' db.execute("INSERT INTO identified_items(id,media_file_id,media_type,title,release_year,content_rating,genre_tags,tmdb_id) VALUES(?,?,?,?,1995,?,?,77)",(i,i,"movie",title,"PG",chr(91)+chr(34)+"western"+chr(34)+chr(93)))',
      'db.commit()',
    ].join('\n'),
    root + '/data/alreadyon.db',
  ]);
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(url + '/admin/channels');
  await page.getByRole('heading', { name: 'Age Restrictions', exact: true }).waitFor();
  let defaults = await (await context.request.get(url+'/api/channel-filter-options')).json();
  assert.deepEqual(defaults.age.map(o=>o.name),['Unrestricted','Teens','Kids']);
  assert.ok(defaults.age.find(o => o.id === 'teen').ratings.includes('TV-Y7-FV'));
  const unrestricted = page.getByRole('row').filter({has:page.getByRole('cell',{name:'Unrestricted',exact:true})});
  const kids = page.getByRole('row').filter({has:page.getByRole('cell',{name:'Kids',exact:true})});
  await unrestricted.getByRole('cell',{name:'Unrestricted',exact:true}).dragTo(kids.getByRole('cell',{name:'Kids',exact:true}));
  await page.waitForFunction(async () => (await (await fetch('/api/channel-filter-options')).json()).age[2].id === 'adult');
  await page.getByRole('button',{name:'Reorder Unrestricted',exact:true}).press('ArrowUp');
  await page.waitForFunction(async () => (await (await fetch('/api/channel-filter-options')).json()).age[1].id === 'adult');
  await unrestricted.getByRole('button',{name:'Edit Unrestricted',exact:true}).click();
  await page.getByRole('dialog',{name:'Configure Age Restriction',exact:true}).getByRole('button',{name:'Cancel',exact:true}).click();
  assert.equal((await (await context.request.get(url+'/api/channel-filter-options')).json()).age[1].id,'adult','Edit must not reorder the row');
  const ageGroup = unrestricted.locator('xpath=ancestor::div[contains(@class,"filter-definition-group")]');
  await ageGroup.getByRole('button',{name:'(Restore Default)',exact:true}).click();
  await page.waitForFunction(async () => (await (await fetch('/api/channel-filter-options')).json()).age[0].id === 'adult');
  await page.waitForFunction(() => ![...document.querySelectorAll('.filter-definition-group .restore-order-link')].length);
  // Touch-only devices activate row dragging after a stationary long press.
  await unrestricted.scrollIntoViewIfNeeded();
  const sourceBox = await unrestricted.getByRole('cell',{name:'Unrestricted',exact:true}).boundingBox();
  const targetBox = await kids.getByRole('cell',{name:'Kids',exact:true}).boundingBox();
  const cdp = await context.newCDPSession(page);
  const sourcePoint = { x: sourceBox.x + sourceBox.width / 2, y: sourceBox.y + sourceBox.height / 2, id: 1 };
  const targetPoint = { x: targetBox.x + targetBox.width / 2, y: targetBox.y + targetBox.height / 2, id: 1 };
  await page.evaluate(() => {
    window.__filterTouchPointerPrevented = false;
    document.addEventListener('pointerdown', event => {
      queueMicrotask(() => { window.__filterTouchPointerPrevented = event.defaultPrevented; });
    }, { once: true });
  });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [sourcePoint] });
  await page.waitForFunction(() => window.__filterTouchPointerPrevented === true);
  await new Promise(resolve => setTimeout(resolve, 550));
  assert.equal(await unrestricted.evaluate(row => row.classList.contains('is-dragging')), true, 'Long press activates touch row dragging');
  assert.equal(await unrestricted.evaluate(row => {
    const event = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
    row.dispatchEvent(event);
    return event.defaultPrevented;
  }), true, 'Long press context menu is suppressed on reorderable rows');
  const lifted = await unrestricted.evaluate(row => ({ transform: getComputedStyle(row).transform, filter: getComputedStyle(row).filter }));
  assert.notEqual(lifted.transform, 'none', 'Long-pressed row lifts above the table');
  assert.notEqual(lifted.filter, 'none', 'Long-pressed row has an elevated shadow');
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [targetPoint] });
  await page.waitForFunction(() => document.querySelector('tr[data-filter-id="young_child"]')?.classList.contains('is-drop-after'));
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await page.waitForFunction(async () => (await (await fetch('/api/channel-filter-options')).json()).age[2].id === 'adult');
  await ageGroup.getByRole('button',{name:'(Restore Default)',exact:true}).click();
  await page.waitForFunction(async () => (await (await fetch('/api/channel-filter-options')).json()).age[0].id === 'adult');
  await page.getByRole('button', { name: '+ Add Age Restriction', exact: true }).click();
  let dialog = page.getByRole('dialog', {
    name: 'Configure Age Restriction',
    exact: true,
  });
  await dialog.getByLabel('Name', { exact: true }).fill('Family Custom');
  assert.notEqual(await dialog.getByLabel('Name',{exact:true}).evaluate(el=>getComputedStyle(el).backgroundColor),'rgb(255, 255, 255)');
  await dialog.getByRole('switch', {name:'PG',exact:true}).click();
  assert.equal(await dialog.getByRole('switch',{name:'Unknown Ratings'}).getAttribute('aria-checked'),'false');
  assert.equal(await dialog.getByRole('button',{name:'Delete',exact:true}).count(),0);
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await dialog.waitFor({ state: 'detached' });
  await page.getByRole('button', { name: '+ Add Era', exact: true }).click();
  dialog = page.getByRole('dialog', { name: 'Configure Era', exact: true });
  await dialog.getByLabel('Name', { exact: true }).fill('My Nineties');
  await dialog.getByLabel('Start Year').fill('*');
  await dialog.getByLabel('End Year').fill('nO LiMiT');
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await dialog.waitFor({ state: 'detached' });
  const add = page.getByRole('button', {
    name: '+ Create Custom Channel',
    exact: true,
  });
  assert.equal(await add.evaluate((e) => e.previousElementSibling.textContent.trim()), 'Set to Auto');
  await add.click();
  dialog = page.getByRole('dialog', {
    name: 'Create Custom Channel',
    exact: true,
  });
  await dialog.locator('input[type=text]').nth(0).fill('250');
  await dialog.locator('input[type=text]').nth(1).fill('Christmas Test');
  await dialog.getByLabel('Age Restrictions', { exact: true }).selectOption({ label: 'Family Custom' });
  await dialog.getByRole('button', { name: 'Era', exact: true }).click();
  await dialog.getByRole('checkbox', { name: 'All', exact: true }).uncheck();
  await dialog.getByRole('checkbox', { name: 'My Nineties', exact: true }).check();
  await dialog.getByRole('button', { name: 'Era', exact: true }).click();
  await dialog.getByLabel('Title Contains', { exact: true }).fill('HOLIDAY');
  await page.keyboard.press('Enter');
  await dialog.getByLabel('Title Contains', { exact: true }).fill('wild west');
  await page.keyboard.press('Tab');
  await dialog.getByRole('button', { name: 'Remove phrase HOLIDAY' }).waitFor();
  await dialog.getByText('Matches 2 shows and movies (2 files).', { exact: true }).waitFor();
  // Match feedback follows commercial settings; long forms retain scroll access to actions.
  assert.equal(await dialog.locator('.channel-filter-preview').evaluate(el =>
    el.nextElementSibling?.classList.contains('admin-table-actions')), true,
    'Match feedback immediately precedes Save/Cancel');
  await dialog.getByRole('button', { name: 'Save', exact: true }).scrollIntoViewIfNeeded();
  assert.equal(await dialog.getByRole('button', { name: 'Save', exact: true }).isVisible(), true);
  await dialog.getByRole('button', { name: 'Save', exact: true }).click();
  await dialog.waitFor({ state: 'detached' });
  const channels = await (await context.request.get(url + '/api/channels')).json();
  const custom = channels.find((c) => c.number === 250);
  assert.equal(custom.itemCount, 2);
  assert.deepEqual(custom.filter.titlePhrases, ['HOLIDAY', 'wild west']);
  let response = await context.request.delete(url + '/api/channel-filter-options/age/' + custom.filter.ageRestrictionId);
  assert.equal(response.status(), 400);
  const options = await (await context.request.get(url + '/api/channel-filter-options')).json();
  const age = options.age.find((a) => a.id === custom.filter.ageRestrictionId);
  assert.equal((await context.request.put(url + '/api/channel-filter-options/age/' + age.id, { data: { ...age, ratings: ['G'] } })).ok(), true);
  const updated = await (await context.request.get(url + '/api/channels/' + custom.id)).json();
  assert.equal(updated.itemCount, 0);
  const base = {
    ...custom.filter,
    ageRestrictionId: 'adult',
    excludedPaths: ['/synthetic/movies'],
  };
  response = await context.request.post(url + '/api/channels/filter-preview', {
    data: base,
  });
  assert.equal((await response.json()).items, 0);
  response = await context.request.post(url + '/api/channels/filter-preview', {
    data: { ...base, excludedPaths: ['/synthetic/movie'] },
  });
  assert.equal((await response.json()).items, 2);
  assert.equal(await page.getByRole('heading', { name: 'Library Coverage' }).count(), 0);

  const configured = page.locator('.admin-card').filter({ has: page.getByRole('heading', { name: 'Configure Channels', exact: true }) });
  await configured.getByRole('button', { name: 'Name', exact: false }).click();
  let names = await configured.locator('tbody tr td:nth-child(3)').allTextContents();
  assert.ok(names[0].includes('After'));
  await configured.getByRole('button', { name: 'Name', exact: false }).click();
  names = await configured.locator('tbody tr td:nth-child(3)').allTextContents();
  assert.ok(names[0].includes('Zenith'));
  assert.equal(await configured.locator('.table-filter-row').count(), 0,
    'Configure Channels has no filter row');
  // Account, graph and server-settings interactions are covered by playback.cjs.
  assert.deepEqual(errors, []);
  console.log('Shared definitions, phrase input, preview/save, reference protection, path boundaries passed.');
})()
  .catch((e) => {
    console.error(e);
    process.exitCode = 1;
  })
  .finally(async () => {
    if (browser) await browser.close();
    if (server) {
      server.kill('SIGTERM');
      await new Promise((resolve) => server.once('exit', resolve));
    }
    fs.rmSync(root, { recursive: true, force: true });
  });
