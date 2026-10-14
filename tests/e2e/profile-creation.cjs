// Profile fixtures exercise the creation shortcut without changing persistent
// accounts. Server account isolation is covered separately by profile API tests.
module.exports = async ({ browser, login, url, assert }) => {
  for (const role of ['admin', 'viewer']) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    const page = await context.newPage();
    await login(page);
    const originals = await (await page.request.get(url + '/api/profiles')).json();
    const base = originals[0];
    let profiles = [
      {...base, id: base.id, name: 'Zulu Active', role},
      {...base, id: base.id + 10000, name: 'beta', role:'viewer'},
      {...base, id: base.id + 10001, name: 'Alpha', role:'viewer'},
    ];
    let creates = 0;
    await page.addInitScript(id => localStorage.setItem('alreadyon.currentUserId', String(id)), base.id);
    await page.route('**/api/profiles', async route => {
      if (route.request().method() === 'POST') {
        creates++;
        const draft = route.request().postDataJSON();
        assert(JSON.stringify(draft) === JSON.stringify({name:'Aaron',role:'viewer',audienceTier:'teen'}), 'Create sends exactly the entered profile fields, without account overrides');
        const saved = {...base, ...draft, id: base.id + 10002}; profiles.push(saved);
        await route.fulfill({status:201,json:saved});
      } else await route.fulfill({json:profiles});
    });
    for (const width of [390,1280]) {
      await page.setViewportSize({width,height:844});
      await page.goto(url + '/profile');
      const trigger = page.locator('.profile-menu-trigger');
      try { await trigger.waitFor({timeout:10000}); }
      catch (failure) { throw new Error(`Profile fixture failed to load at ${page.url()}: ${await page.locator('body').innerText()}`, {cause:failure}); }
      await trigger.click(); await page.getByRole('button',{name:'Switch Profile',exact:true}).click();
      const names = page.locator('.profile-menu-profile .profile-menu-name');
      assert(JSON.stringify(await names.allTextContents()) === JSON.stringify(creates ? ['Zulu Active','Aaron','Alpha','beta'] : ['Zulu Active','Alpha','beta']), 'Current profile first, other names alphabetically sorted');
      const add = page.getByRole('button',{name:'Add Profile',exact:true});
      if (role === 'viewer') { assert(await add.count() === 0,'Viewer profiles cannot see Add Profile'); continue; }
      const back = await page.getByRole('button',{name:'← Back',exact:true}).boundingBox();
      const addBox = await add.boundingBox();
      assert(Math.abs(back.y-addBox.y)<2 && addBox.x>back.x, 'Add Profile is inline to the right of Back');
      await add.click();
      const dialog = page.getByRole('dialog',{name:'Add Profile',exact:true});await dialog.waitFor();
      assert(await page.locator('.profile-menu-panel').count() === 0,'Opening the modal closes the menu');
      assert(JSON.stringify(await dialog.getByLabel('Role',{exact:true}).locator('option').allTextContents())===JSON.stringify(['Admin','Viewer']),'Role options are alphabetically sorted');
      assert(JSON.stringify(await dialog.getByLabel('Audience',{exact:true}).locator('option').allTextContents())===JSON.stringify(['Kids','Teen','Unrestricted']),'Audience options are alphabetically sorted');
      const box = await dialog.boundingBox();
      assert(Math.abs(box.x+box.width/2-width/2)<2 && Math.abs(box.y+box.height/2-422)<2,'Modal is horizontally and vertically centered');
      assert(await dialog.getByLabel('Name',{exact:true}).getAttribute('autocomplete') === 'off','Modal follows autofill policy');
      await dialog.getByRole('button',{name:'Cancel',exact:true}).click();
      await dialog.waitFor({state:'hidden'});
      assert(await trigger.evaluate(e=>e===document.activeElement),'Closing restores profile-control focus');
      if (!creates) {
        assert(creates===0,'Cancel makes no create request');
        await trigger.click();await page.getByRole('button',{name:'Switch Profile',exact:true}).click();await add.click();
        await dialog.getByLabel('Name',{exact:true}).fill('  Aaron  ');
        await dialog.getByLabel('Role',{exact:true}).selectOption('viewer');
        await dialog.getByLabel('Audience',{exact:true}).selectOption('teen');
        await dialog.getByRole('button',{name:'Create',exact:true}).click();await dialog.waitFor({state:'hidden'});
        assert(creates===1,'Create submits once');
        await trigger.click();await page.getByRole('button',{name:'Switch Profile',exact:true}).click();
        assert(JSON.stringify(await names.allTextContents())===JSON.stringify(['Zulu Active','Aaron','Alpha','beta']),'New profile appears immediately without reloading');
        await page.keyboard.press('Escape');
      }
    }
    await context.close();
  }
  console.log('PASS: Admin-only profile creation, centered modal, Cancel/Create, focus and active-first alphabetical list');
};
