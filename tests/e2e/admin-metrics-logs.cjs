// Isolated browser fixtures for per-core CPU, disk I/O, log following and dashboard cost.
module.exports = async function adminMetricsAndLogs({browser, login, url, assert, delay, seed}) {
  // Folder-rescan scenarios can replace fixture schedules; restore this scenario’s guide.
  seed();
  const context = await browser.newContext({viewport:{width:1440,height:1000}});
  const page = await context.newPage();
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await login(page);
  await page.goto(url+'/admin/users');
  await page.locator('.admin-card').first().waitFor();
  const cdp=await context.newCDPSession(page);await cdp.send('Performance.enable');
  const cpu=async()=>Object.fromEntries((await cdp.send('Performance.getMetrics')).metrics.map(m=>[m.name,m.value]));
  const baseline=await cpu();await delay(8000);const idle=await cpu();
  let requests=0;
  await page.route('**/api/server/metrics?*',route=>{
    requests++;
    const now=Math.floor(Date.now()/1000);
    const point=(i)=>({at:now-(60-i)*15,cpu:20+i%40,ram:40,networkInterface:'eth0',networkScope:'host',ingressMbps:2,egressMbps:4,
      cpuCores:Array.from({length:8},(_,j)=>({core:j,utilization:i===30?null:(i*3+j*10)%100})),cpuLoad:1.5,cpuPressure:2.25,
      memoryUsedBytes:4e9,memoryLimitBytes:8e9,memoryLimited:true,diskReadMBps:i===30?null:i/2,diskWriteMBps:i/4,ioPressure:i%100,
      disk:41.2,diskUsedBytes:32e9,diskTotalBytes:128e9,diskPath:'/config',diskScope:'container',mediaUsedBytes:12.2e12,mediaTotalBytes:16e12});
    return route.fulfill({json:{intervalSeconds:15,points:[point(-10), ...Array.from({length:60},(_,i)=>point(i))],current:point(60)}});
  });
  await page.route('**/api/server/activity',route=>route.fulfill({json:{sessions:[],processes:[]}}));
  await page.goto(url+'/admin/dashboard');
  await page.locator('[data-metric="disk"] .metric-stats').getByText('Config: 32GB / 128GB').waitFor();
  const panels=page.locator('.metrics-grid .metric-panel');
  assert(JSON.stringify(await panels.evaluateAll(es=>es.map(e=>e.dataset.metric)))===JSON.stringify(['cpu','ram','disk','bandwidth']),'Performance panel order');
  const positions=await panels.evaluateAll(es=>es.map(e=>({x:e.getBoundingClientRect().x,y:e.getBoundingClientRect().y})));
  assert(positions[0].y===positions[1].y&&positions[2].y===positions[3].y&&positions[0].x===positions[2].x&&positions[2].y>positions[0].y,'Two-by-two layout');
  const cpuPanel=page.locator('[data-metric="cpu"]');
  const gutter = await cpuPanel.evaluate(panel => ({
    left: Number(panel.querySelector('.metric-gridline').getAttribute('x1')),
    label: Number(panel.querySelector('text').getAttribute('x')),
    points: [...panel.querySelectorAll('circle')].map(dot => Number(dot.getAttribute('cx'))),
  }));
  assert(gutter.label + 10 < gutter.left && gutter.points.every(x => x >= gutter.left),
    'Old history samples must not overlap the y-axis gutter: ' + JSON.stringify(gutter));

  assert(await cpuPanel.locator('.metric-line').count()===8,'Each logical CPU needs its own line');
  const coreColors=await cpuPanel.locator('.metric-line').evaluateAll(es=>es.map(e=>getComputedStyle(e).stroke));
  assert(new Set(coreColors).size===8,'Core lines should be distinguishable');
  assert((await cpuPanel.locator('.metric-line').first().getAttribute('d')).split('M').length===3,'Missing core samples should break the line');
  const statRows=async metric=>page.locator(`[data-metric="${metric}"] .metric-stats > div`).evaluateAll(es=>es.map(e=>[...e.children].map(c=>c.textContent).join('  ')));
  assert(JSON.stringify(await statRows('cpu'))===JSON.stringify(['Load: 1.50  Pressure: 2.3%  Utilization: 40%']),'CPU stat row: '+JSON.stringify(await statRows('cpu')));
  assert(!/°C|Temperature/i.test(await page.locator('.metrics-grid').innerText()),'CPU temperatures are removed');
  assert(JSON.stringify(await statRows('ram'))===JSON.stringify(['Usage: 4.0GB / 8.0GB  Utilization: 40%']),'RAM stat row: '+JSON.stringify(await statRows('ram')));
  assert(JSON.stringify(await statRows('disk'))===JSON.stringify(['Read: 30.0MB/s  Write: 15.0MB/s','Utilization: 41%  IO Pressure: 60.0%','Config: 32GB / 128GB  Media: 12.2TB / 16TB']),'Disk stat rows: '+JSON.stringify(await statRows('disk')));
  for (const metric of ['cpu','ram','disk']) assert(await page.locator(`[data-metric="${metric}"] .metric-title span`).count()===0,'Utilization moved out of the title');
  await page.waitForFunction(()=>/Latency: (<1|\d+)ms/.test(document.querySelector('.bandwidth-latency')?.textContent||''),null,{timeout:5000});
  const disk=page.locator('[data-metric="disk"]');
  assert(await disk.locator('.metric-line').count()===3,'Disk read, write and I/O pressure lines');
  assert((await disk.locator('path.metric-line-diskRead').getAttribute('d')).split('M').length===3,'Missing disk samples should break the line');
  const checkDiskLegend = async panel => {
    const legendBottom = await panel.locator('.metric-stats').evaluate(e => e.getBoundingClientRect().bottom);
    const plotTop = await panel.locator('.metric-gridline').evaluateAll(es=>Math.min(...es.map(e=>e.getBoundingClientRect().top)));
    assert(legendBottom < plotTop, 'Disk legend should stay above plotted history');
  };
  await checkDiskLegend(disk);
  await disk.click();await page.getByRole('dialog',{name:'Disk graph',exact:true}).waitFor();
  await checkDiskLegend(page.getByRole('dialog',{name:'Disk graph',exact:true}).locator('.metric-panel'));
  await page.keyboard.press('Escape');
  const start=await cpu();const requestStart=requests;await delay(12000);const finish=await cpu();
  console.log('PERF browser:',JSON.stringify({idleSeconds:idle.Timestamp-baseline.Timestamp,idleTaskSeconds:idle.TaskDuration-baseline.TaskDuration,dashboardSeconds:finish.Timestamp-start.Timestamp,dashboardTaskSeconds:finish.TaskDuration-start.TaskDuration,scriptSeconds:finish.ScriptDuration-start.ScriptDuration,layoutSeconds:finish.LayoutDuration-start.LayoutDuration,metricsResponses:requests-requestStart,cores:8,pointsPerCore:60}));
  await page.setViewportSize({width:375,height:900});
  const mobile=await panels.evaluateAll(es=>es.map(e=>e.getBoundingClientRect().x));
  assert(new Set(mobile).size===1,'Narrow metrics should stack');
  await page.setViewportSize({width:1440,height:1000});
  let generation=0;
  await page.route('**/api/logs?*',route=>{
    generation++;
    return route.fulfill({json:{timeZone:'America/New_York',entries:Array.from({length:100},(_,i)=>({time:new Date(1800000000000+generation*100000+i*1000).toISOString(),level:'info',message:`generation ${generation} line ${i} `+'Synthetic log entry with wrapping. '.repeat(12)}))}});
  });
  await page.goto(url+'/admin/logs');
  await page.locator('.log-line').last().waitFor();
  const atBottom=()=>page.locator('.log-viewer').evaluate(e=>Math.abs(e.scrollHeight-e.clientHeight-e.scrollTop)<2);
  assert(await atBottom(),'Initial logs should scroll to bottom');
  await page.locator('.log-viewer').evaluate(e=>{e.scrollTop=0});
  const previous=generation;
  await page.waitForFunction(g=>document.querySelector('.log-line-message')?.textContent.includes(`generation ${g+1} `),previous);
  assert(await atBottom(),'Updated logs should follow latest entry');
  await page.getByRole('button',{name:'Pause',exact:true}).click();
  await page.locator('.log-viewer').evaluate(e=>{e.scrollTop=0});
  const pausedGeneration=generation;await delay(3500);
  assert(generation===pausedGeneration&&!await atBottom(),'Pause must preserve scroll and stop fetching');
  await page.getByRole('button',{name:'Resume',exact:true}).click();
  await page.waitForFunction(g=>document.querySelector('.log-line-message')?.textContent.includes(`generation ${g+1} `),pausedGeneration);
  assert(await atBottom(),'Resume should follow again');
  await page.getByRole('button',{name:'Pause',exact:true}).click();
  await page.reload();await page.locator('.log-line').last().waitFor();
  assert(await page.getByRole('button',{name:'Pause',exact:true}).count()===1&&await atBottom(),'Remount must reset following');
  assert(errors.length===0,JSON.stringify(errors));
  await context.close();
  console.log('PASS: disk series, gaps, graph layout/modal and log initial/update/pause/resume/remount following');
};
