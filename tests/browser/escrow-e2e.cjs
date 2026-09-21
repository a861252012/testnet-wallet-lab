'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { chromium } = require('playwright');

(async () => {
  assert.equal(process.env.E2E_BACKEND, 'simulated');
  const buyerURL = process.env.E2E_BUYER_URL, sellerURL = process.env.E2E_SELLER_URL;
  for (const raw of [buyerURL,sellerURL]) {
    const url = new URL(raw);
    assert.equal(url.protocol,'http:');
    assert.ok(['127.0.0.1','[::1]'].includes(url.hostname));
  }
  const browser = await chromium.launch({headless:true});
  const context = await browser.newContext({viewport:{width:1280,height:900},locale:'zh-TW'});
  const origins = new Set([new URL(buyerURL).origin,new URL(sellerURL).origin]);
  await context.route('**/*',route=>origins.has(new URL(route.request().url()).origin)?route.continue():route.abort());
  const buyer = await context.newPage(), seller = await context.newPage();
  const errors = [];
  for (const page of [buyer,seller]) page.on('pageerror',error=>errors.push(error.message));
  const shots = path.resolve(__dirname,'../../test-results/escrow');
  await fs.mkdir(shots,{recursive:true});
  async function open(page,url) {
    await page.goto(url+'/#vault-panel');
    await page.locator('#wallet-dashboard').waitFor({state:'visible'});
    await page.locator('#contract-tab-escrow').click();
    await page.locator('#escrow-enabled').waitFor({state:'visible'});
  }
  async function preview(page) {
    await page.locator('#escrow-fund-preview').click();
    await page.locator('#send-confirmation').waitFor({state:'visible'});
  }
  async function confirm(page,action,amount) {
    let sends = 0;
    let historyFailures = 0;
    const countSend = request=>{if(new URL(request.url()).pathname==='/api/wallet/send') sends++;};
    const unavailableHistory = route=>{
      if (sends > 0) historyFailures++;
      return route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'fixture history unavailable'})});
    };
    await page.route('**/api/wallet/history',unavailableHistory);
    page.on('request',countSend);
    let data;
    try {
      await page.locator('#send-password').fill(process.env.E2E_PASSWORD);
      const [response] = await Promise.all([
        page.waitForResponse(response=>new URL(response.url()).pathname==='/api/wallet/send'),
        page.locator('#confirm-send-button').click(),
      ]);
      assert.equal(response.status(),200,await response.text());
      data = await response.json();
      assert.equal(data.action,action);assert.equal(data.amount,amount);
      await page.locator('#send-confirmation').waitFor({state:'hidden'});
      await page.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled&&!document.querySelector('#confirm-send-button').disabled);
      assert.ok(historyFailures>0,'must exercise history failure after sending');
      assert.ok((await page.locator('#wallet-error').textContent()).includes('fixture history unavailable'));
      assert.equal(await page.locator('#send-feedback').isVisible(),true,'history failure must not hide submission evidence');
      assert.ok((await page.locator('#send-feedback').innerText()).includes(data.hash));
      assert.ok((await page.locator('#send-feedback a').getAttribute('href')).endsWith(data.hash));
      assert.equal(await page.locator('#send-password').inputValue(),'');
      assert.equal(sends,1,'history failure must not trigger another send');
    } finally {
      await page.unroute('**/api/wallet/history',unavailableHistory);
      page.off('request',countSend);
    }
    await page.locator('#escrow-refresh').click();
    await page.waitForFunction(hash=>Array.from(document.querySelectorAll('#escrow-history a')).some(link=>link.href.endsWith(hash)),data.hash);
    await page.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    assert.ok((await page.locator('#send-feedback').innerText()).includes(data.hash),'history recovery retains submission evidence');
    return data;
  }
  async function funded(reference,amount) {
    await buyer.locator('#escrow-new').evaluate(el=>el.open=true);
    await buyer.locator('#escrow-reference').fill(reference);
    await buyer.locator('#escrow-seller').fill(process.env.E2E_SELLER);
    await buyer.locator('#escrow-amount').fill(amount);
    await preview(buyer);
    assert.equal(await buyer.locator('#confirm-title').textContent(),'確認本次 USDC 授權');
    assert.match(await buyer.locator('#quote-details').textContent(),/只授權，不會付款/);
    await confirm(buyer,'approve',amount);
    assert.equal(await buyer.locator('#escrow-order-state').textContent(),'尚未付款');
    await preview(buyer);
    assert.equal(await buyer.locator('#confirm-title').textContent(),'確認付款至合約');
    const details = await buyer.locator('#quote-details').textContent();
    assert.ok(details.includes(reference) && details.includes(process.env.E2E_SELLER));
    assert.ok(details.includes('目前錢包 → 託管合約'));
    await confirm(buyer,'escrow_fund',amount);
    await buyer.waitForFunction(()=>document.querySelector('#escrow-order-state').textContent==='款項由合約保管');
  }
  try {
    await open(buyer,buyerURL);
    assert.equal(await buyer.evaluate(()=>getComputedStyle(document.querySelector('#contract-tab-vault')).backgroundColor!==getComputedStyle(document.querySelector('#contract-tab-escrow')).backgroundColor),true,'selected tab is visually distinct');
    await buyer.locator('#escrow-reference').fill('browser-refund');
    await buyer.locator('#escrow-seller').fill(process.env.E2E_BUYER);
    await buyer.locator('#escrow-amount').fill('5');
    await buyer.locator('#escrow-fund-preview').click();
    await buyer.locator('#escrow-error').waitFor({state:'visible'});
    assert.match(await buyer.locator('#escrow-error').textContent(),/另一個錢包/);
    assert.equal(await buyer.locator('#send-confirmation').isVisible(),false);
    await funded('browser-refund','5');
    assert.equal(await buyer.locator('#escrow-release').isVisible(),true);
    assert.equal(await buyer.locator('#escrow-refund').isVisible(),false);
    await buyer.evaluate(()=>scrollTo(0,0));
    await buyer.screenshot({path:path.join(shots,'desktop-funded.png'),fullPage:true});
    await buyer.reload();
    await buyer.locator('#contract-tab-escrow').click();
    await buyer.waitForFunction(()=>document.querySelector('#escrow-order-state').textContent==='款項由合約保管');
    assert.equal(await buyer.locator('#escrow-reference').inputValue(),'browser-refund');
    await buyer.locator('#escrow-new summary').click();
    await buyer.locator('#escrow-fund-preview').click();
    await buyer.locator('#escrow-error').waitFor({state:'visible'});
    assert.match(await buyer.locator('#escrow-error').textContent(),/已付款/);
    // Loading failures must not preserve actionable stale state.
    await buyer.route('**/api/wallet/escrow',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'fixture RPC unavailable'})}));
    await buyer.locator('#escrow-refresh').click();
    await buyer.waitForFunction(()=>document.querySelector('#escrow-status').textContent==='暫時無法讀取付款狀態，請重試。');
    assert.equal(await buyer.locator('#escrow-enabled').isVisible(),false);
    await buyer.unroute('**/api/wallet/escrow');
    await buyer.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    await buyer.locator('#escrow-refresh').click();
    await buyer.locator('#escrow-enabled').waitFor({state:'visible'});

    await open(seller,sellerURL);
    await seller.locator('#escrow-search summary').click();
    await seller.locator('#escrow-lookup-buyer').fill(process.env.E2E_BUYER);
    await seller.locator('#escrow-lookup-reference').fill('browser-refund');
    await seller.locator('#escrow-lookup').click();
    await seller.locator('#escrow-refund').waitFor({state:'visible'});
    assert.equal(await seller.locator('#escrow-release').isVisible(),false);
    await seller.locator('#escrow-refund').click();
    await seller.locator('#send-confirmation').waitFor({state:'visible'});
    assert.match(await seller.locator('#quote-details').textContent(),/託管合約 → 原付款人/);
    await seller.locator('#cancel-send').click();
    assert.equal(await seller.locator('#escrow-order-state').textContent(),'款項由合約保管');
    await seller.locator('#escrow-refund').click();
    await seller.locator('#send-confirmation').waitFor({state:'visible'});
    await confirm(seller,'escrow_refund','5');
    await seller.waitForFunction(()=>document.querySelector('#escrow-order-state').textContent==='已退回付款人');
    assert.equal(await seller.locator('#escrow-refund').isVisible(),false);
    await seller.setViewportSize({width:390,height:844});
    await seller.evaluate(()=>scrollTo(0,0));
    await seller.screenshot({path:path.join(shots,'mobile-refunded.png'),fullPage:true});
    assert.equal(await seller.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,'mobile no horizontal overflow');

    await buyer.locator('#escrow-refresh').click();
    await buyer.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    await funded('browser-release','3.25');
    await buyer.locator('#escrow-release').click();
    await buyer.locator('#send-confirmation').waitFor({state:'visible'});
    assert.match(await buyer.locator('#quote-details').textContent(),/託管合約 → 收款人/);
    // Lose the HTTP response after the Go backend signs and sends. Repeating confirms the same quote.
    let originalHash;
    await buyer.route('**/api/wallet/send',async route=>{
      const response=await route.fetch();const transaction=await response.json();originalHash=transaction.hash;
      await route.abort('failed');
    },{times:1});
    await buyer.locator('#send-password').fill(process.env.E2E_PASSWORD);
    await buyer.locator('#confirm-send-button').click();
    await buyer.locator('#confirm-error').waitFor({state:'visible'});
    assert.equal(await buyer.locator('#send-confirmation').isVisible(),true);
    await buyer.waitForFunction(()=>!document.querySelector('#confirm-send-button').disabled);
    const recovered=await confirm(buyer,'escrow_release','3.25');
    assert.equal(recovered.hash,originalHash,'lost response retry uses same hash');
    await buyer.waitForFunction(()=>document.querySelector('#escrow-order-state').textContent==='已放款給收款人');
    assert.equal(await buyer.locator('#escrow-release').isVisible(),false);
    // Keyboard tabs and the original vault panel remain available.
    await buyer.locator('#contract-tab-escrow').focus();await buyer.keyboard.press('ArrowLeft');
    assert.equal(await buyer.locator('#vault-eth-content').isVisible(),true);
    await buyer.keyboard.press('ArrowRight');
    assert.equal(await buyer.locator('#escrow-content').isVisible(),true);
    await buyer.waitForFunction(()=>!document.querySelector('#escrow-refresh').disabled);
    await buyer.evaluate(()=>scrollTo(0,0));
    await buyer.screenshot({path:path.join(shots,'desktop-released.png'),fullPage:true});
    for (const locale of ['en','zh-CN','zh-TW']) {
      await buyer.locator('#language-select').selectOption(locale);
      for (const theme of ['dark','light']) {
        if (await buyer.locator('html').getAttribute('data-theme') !== theme) await buyer.locator('#theme-toggle').click();
        for (const width of [375,1280]) {
          await buyer.setViewportSize({width,height:900});
          assert.equal(await buyer.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,locale+' '+theme+' '+width+' overflow');
        }
      }
      if (locale==='en') assert.ok(!/[\u3400-\u9fff]/.test(await buyer.locator('#escrow-content').innerText()),'English escrow translated');
    }
    await buyer.setViewportSize({width:390,height:844});
    await buyer.locator('#theme-toggle').click();
    await buyer.evaluate(()=>scrollTo(0,0));
    await buyer.screenshot({path:path.join(shots,'mobile-dark-released.png'),fullPage:true});
    assert.deepEqual(errors,[]);
    console.log('PASS: Chromium + Go + simulated EVM: approvals, payment, refund, release, draft recovery, lost-response retry, roles, errors, mobile and keyboard; chain receipts checked by Go.');
  } finally {await context.close();await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
