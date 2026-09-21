'use strict';
const {chromium}=require('../../../tests/browser/node_modules/playwright');
const fs=require('node:fs/promises');
const path=require('node:path');
const assert=require('node:assert/strict');
(async()=>{
 const revision='3a55ab854701074f6fb796ba23fe7953b67fbd8e';
 const browser=await chromium.launch({headless:true});
 try {
  const context=await browser.newContext({baseURL:'https://wallet.tedlin.fyi',viewport:{width:1280,height:900}});
  const blocked=[];
  await context.route('**/*',route=>{
   const r=route.request();
   if(!['GET','HEAD'].includes(r.method())){blocked.push({method:r.method(),path:new URL(r.url()).pathname});return route.abort();}
   return route.continue();
  });
  const page=await context.newPage();const errors=[];
  page.on('pageerror',e=>errors.push(String(e)));
  const health=await context.request.get('/healthz');
  assert.equal(health.status(),200);assert.equal(health.headers()['x-app-version'],revision);
  await page.goto('/#vault-panel');
  await page.waitForFunction(()=>document.querySelector('#vault-status').textContent===window.FlowI18n.t('此環境尚未開放合約操作'));
  const vault=await context.request.get('/api/wallet/vault');
  assert.equal(vault.status(),200);const config=await vault.json();assert.equal(config.enabled,false);
  const hidden={};for(const id of ['vault-form','vault-summary','vault-history-section']){hidden[id]=!await page.locator('#'+id).isVisible();assert.ok(hidden[id]);}
  await page.screenshot({path:path.join(__dirname,'desktop-vault.png'),fullPage:true});
  await page.setViewportSize({width:375,height:812});
  await page.locator('#menu-toggle').click();
  await page.locator('#app-sidebar a[href="#receive-panel"]').click();
  await page.waitForFunction(()=>document.body.dataset.view==='receive-panel'&&!document.body.classList.contains('menu-open'));
  await page.locator('#menu-toggle').click();
  await page.locator('#app-sidebar a[href="#vault-panel"]').click();
  await page.waitForFunction(()=>document.body.dataset.view==='vault-panel'&&!document.body.classList.contains('menu-open'));
  await page.waitForFunction(()=>document.querySelector('#vault-status').textContent===window.FlowI18n.t('此環境尚未開放合約操作'));
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await page.screenshot({path:path.join(__dirname,'mobile-vault.png'),fullPage:true});
  const end=await context.request.get('/healthz');assert.equal(end.headers()['x-app-version'],revision);
  assert.deepEqual(errors,[]);assert.deepEqual(blocked.filter(r=>r.method!=='POST'||r.path!=='/api/wallet/token'),[]);
  const evidence={at:new Date().toISOString(),revision,health:await end.json(),vault:config,hidden,desktop:[1280,900],mobile:[375,812],mobileNavigation:'passed',horizontalOverflow:false,pageErrors:errors,blockedPostRequests:blocked,writeRequestsSent:0};
  await fs.writeFile(path.join(__dirname,'ui-evidence.json'),JSON.stringify(evidence,null,2)+'\n');
  console.log(JSON.stringify(evidence,null,2));
 } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
