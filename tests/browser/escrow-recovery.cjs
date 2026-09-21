'use strict';
const assert = require('node:assert/strict');
const { chromium } = require('playwright');

(async () => {
  assert.equal(process.env.E2E_BACKEND,'simulated');
  const origin = new URL(process.env.E2E_BUYER_URL);
  assert.equal(origin.protocol,'http:');
  assert.ok(['127.0.0.1','[::1]'].includes(origin.hostname));
  const browser = await chromium.launch({headless:true});
  const context = await browser.newContext({viewport:{width:390,height:844},locale:'zh-TW'});
  await context.route('**/*',route=>new URL(route.request().url()).origin===origin.origin?route.continue():route.abort());
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror',error=>{errors.push(error.message);console.error(error.stack);});
  try {
    await page.goto(origin.origin+'/#vault-panel');
    await page.locator('#wallet-dashboard').waitFor({state:'visible'});
    await page.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    await page.locator('#contract-tab-escrow').click();
    await page.waitForFunction(()=>!document.querySelector('#escrow-refresh').disabled);
    assert.equal(await page.locator('#escrow-lookup-reference').inputValue(),'','fresh session has no draft');
    await page.locator('#escrow-history button[data-order-id]').click();
    await page.locator('#escrow-release').waitFor({state:'visible'});
    assert.equal(await page.locator('#escrow-order-state').textContent(),'款項由合約保管');
    assert.equal(await page.locator('#escrow-lookup-reference').inputValue(),process.env.E2E_ORDER_ID);
    // A recovered 66-character legacy key also survives an ordinary reload.
    await page.reload();
    await page.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    await page.locator('#contract-tab-escrow').click();
    await page.locator('#escrow-release').waitFor({state:'visible'});
    assert.equal(await page.locator('#escrow-lookup-reference').inputValue(),process.env.E2E_ORDER_ID);
    // Older entries remain reachable through the full history, outside the five recent payments.
    await page.evaluate(()=>sessionStorage.clear());
    await page.goto(origin.origin+'/#history-panel');
    await page.locator('#wallet-history button[data-order-id]').click();
    await page.locator('#escrow-release').waitFor({state:'visible'});
    assert.equal(await page.locator('#escrow-order-state').textContent(),'款項由合約保管');
    await page.locator('#escrow-release').click();
    await page.locator('#send-confirmation').waitFor({state:'visible'});
    assert.ok((await page.locator('#quote-details').textContent()).includes(process.env.E2E_ORDER_ID));
    await page.locator('#cancel-send').click();
    for (const locale of ['en','zh-CN','zh-TW']) {
      await page.locator('#language-select').selectOption(locale);
      for (const theme of ['dark','light']) {
        if (await page.locator('html').getAttribute('data-theme')!==theme) await page.locator('#theme-toggle').click();
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,locale+' '+theme+' overflow');
      }
      if (locale==='en') assert.ok(!/[\u3400-\u9fff]/.test(await page.locator('#escrow-content').innerText()));
    }
    assert.deepEqual(errors,[]);
    console.log('PASS: fresh session and restarted journal recover an order from recent and full history; lookup, settlement preview, reload, mobile and translations verified.');
  } finally { await browser.close(); }
})().catch(error=>{console.error(error.message);process.exitCode=1;});
