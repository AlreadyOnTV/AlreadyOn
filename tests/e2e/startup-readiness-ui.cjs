// Intercept every API/media request: the dev site only serves application assets.
// Real decoding after startup readiness is covered by startup-benchmarks.cjs.
const { chromium } = require('../../web/node_modules/playwright');
const assert = require('node:assert/strict');
(async () => {
 const browser = await chromium.launch({headless:true});
 try {
  const context = await browser.newContext({ignoreHTTPSErrors:true});
  const page = await context.newPage();
  const errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  let ready=false, attempts=0, tunes=0;
  const now=Date.now();
  const profile={id:1,name:'Synthetic startup viewer',role:'admin',audienceTier:'adult',hasPin:false,lastWatchedChannelId:101};
  await page.route(url=>url.pathname.startsWith('/api/'),route=>{
   const path=new URL(route.request().url()).pathname;
   const method=route.request().method();
   if(path==='/api/auth/status')return route.fulfill({json:{authenticated:true,setupRequired:false,username:'synthetic',role:'owner'}});
   if(path==='/api/profiles')return route.fulfill({json:[profile]});
   if(path==='/api/profiles/1')return route.fulfill({json:profile});
   if(path==='/api/status/startup')return route.fulfill({json:{phase:ready?'ready':'benchmarking',active:!ready,startupComplete:ready,trigger:'startup',mediaRoots:1,library:{filesFound:1,itemsIdentified:1,itemsClassified:1,channelsWithContent:1}}});
   if(path==='/api/guide')return route.fulfill({json:[{channelId:101,number:101,name:'Synthetic channel',programs:[{mediaFileId:1,kind:'program',mediaType:'movie',title:'Synthetic program',startsAt:new Date(now-60000).toISOString(),endsAt:new Date(now+3600000).toISOString()}]}]});
   if(path==='/api/playback/probe')return route.fulfill({status:503,json:{code:'server_starting',error:'Server starting, please wait.'}});
   if(path==='/api/playback/sessions' && method==='POST'){
    attempts++;
    if(!ready)return route.fulfill({status:503,headers:{'Retry-After':'2'},json:{code:'server_starting',error:'Server starting, please wait.'}});
    return route.fulfill({status:201,json:{id:'synthetic',token:'synthetic',channelId:101,mediaUrl:new URL('/media/101',page.url()).href,expiresAt:new Date(now+3600000).toISOString()}});
   }
   if(path==='/api/channels/101/tune'){
    tunes++;
    return route.fulfill({status:503,json:{code:'fixture_ready',error:'Synthetic playback admission reached'}});
   }
   if(['/api/playback/activity','/api/playback/sessions'].includes(path) && method==='DELETE')return route.fulfill({status:204});
   if(path==='/api/playback/activity' && method==='POST')return route.fulfill({json:{}});
   if(method!=='GET'){errors.push(`Unexpected write ${method} ${path}`);return route.abort();}
   return route.fulfill({json:{}});
  });
  await page.route('**/media/**',route=>route.abort());
  await page.goto((process.argv[2]||'https://localhost:5173')+'/');
  await page.locator('.guide-slot-box').first().click();
  const waiting=page.locator('.playback-message');
  await waiting.filter({hasText:'Server starting, please wait.'}).waitFor();
  assert.equal(await waiting.getAttribute('role'),'status');
  await page.waitForResponse(response=>response.url().endsWith('/api/playback/sessions')&&response.status()===503);
  assert(attempts>=2,'Waiting player retries without a click/reload');
  assert.equal(await page.getByRole('dialog').count(),0,'No sign-in interruption');
  ready=true;
  await page.getByRole('alert').filter({hasText:'Synthetic playback admission reached'}).waitFor();
  assert(tunes>0,'Readiness automatically advances to tuning');
  assert.deepEqual(errors,[]);
  await page.goto((process.argv[2]||'https://localhost:5173')+'/profile');
  await context.close();
  console.log('PASS: local dev-site startup waiting text, status role, repeated readiness retries and automatic tuning with API-isolated fixtures');
 } finally {await browser.close();}
})().catch(error=>{console.error(error);process.exit(1)});
