// Isolated monitoring fixtures: this test never reads or changes preview accounts.
const { chromium } = require('../../web/node_modules/playwright');
const assert = require('node:assert/strict');
(async () => {
 const browser = await chromium.launch({headless:true});
 try {
  const page = await browser.newPage({ignoreHTTPSErrors:true});
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route(url=>url.pathname.startsWith('/api/'),route=>{
   const path=new URL(route.request().url()).pathname;
   if(route.request().method()!=='GET')return route.abort();
   if(path==='/api/auth/status')return route.fulfill({json:{authenticated:true,setupRequired:false,username:'synthetic',role:'owner'}});
   if(path==='/api/profiles')return route.fulfill({json:[{id:1,name:'Synthetic owner',role:'admin',audienceTier:'adult',hasPin:false}]});
   if(path==='/api/settings/transcoding/capacity')return route.fulfill({json:{profiles:[
    {sourceClass:'h264-sdr-1080p',height:1080,fps:30,threads:2,hardwareDecode:true,validatedStreams:4,verified:true},
    {sourceClass:'hevc-main10-4k-hlg',height:1080,fps:30,threads:2,hardwareDecode:true,toneMapper:'cpu',validatedStreams:0,verified:false}
   ]}});
   return route.fulfill({status:503,json:{error:'Unrelated monitoring fixture unavailable'}});
  });
  await page.goto((process.argv[2]||'https://localhost:5173')+'/admin/dashboard');
  await page.getByText('Validated Transcoding Capacity',{exact:true}).waitFor();
  await page.getByRole('cell',{name:'Unverified',exact:true}).waitFor();
  const row=page.getByRole('row').filter({hasText:'1080p H.264 SDR'});
  assert.match(await row.innerText(),/1080p30/);
  assert.equal(await row.getByRole('cell').last().innerText(),'4');
  assert.deepEqual(errors,[]);
  console.log('PASS: dev Dashboard displays per-pipeline concurrency and unverified paths');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
