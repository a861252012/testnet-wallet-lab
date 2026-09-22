'use strict';
const assert = require('node:assert/strict');

module.exports = async function sendRecovery(existingPage) {
  const base = new URL(existingPage.url()).origin;
  const context = await existingPage.context().browser().newContext({locale:'zh-TW'});
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  const page = await context.newPage();
  const address = '0x1111111111111111111111111111111111111111';
  const hash = '0x'+'a'.repeat(64);
  let history = [], sends = [], failHistory = false, scenario = 0;
  const errors = [];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/api/wallet/history',route=>failHistory?route.fulfill({status:502,contentType:'text/html',body:'Bad Gateway'}):route.fulfill({json:{transactions:history}}));
  async function preview() {
    await page.goto(base+'/?recovery='+ (++scenario) +'#send-panel');
    await page.waitForFunction(()=>!document.querySelector('#refresh-wallet').disabled);
    await page.locator('#send-to').fill(address);
    await page.locator('#send-amount').fill('0.001');
    await page.locator('#send-form button[type="submit"]').click();
    await page.locator('#send-confirmation').waitFor({state:'visible'});
  }
  async function submit() {
    await page.locator('#send-password').fill('isolated-fixture-password');
    await page.locator('#confirm-send-button').click();
    await page.waitForFunction(()=>!document.querySelector('#confirm-send-button').disabled);
  }
  try {
    for (const failure of ['html502','invalid200','disconnect','empty503','null200']) {
      history = []; sends = [];
      await preview();
      await page.route('**/api/wallet/send',async route=>{
        const body = route.request().postDataJSON();
        sends.push(body.quoteId);
        if (!history.length) history.push({quoteId:body.quoteId,hash,state:'succeeded',action:'eth',to:address,amount:'0.001',symbol:'ETH',createdAt:new Date().toISOString()});
        if (sends.length>1) return route.fulfill({json:history[0]});
        if (failure==='disconnect') return route.abort('failed');
        return route.fulfill({status:failure==='html502'?502:failure==='empty503'?503:200,contentType:'text/html',body:failure==='null200'?'null':failure==='empty503'?'':'<!DOCTYPE html><title>Bad Gateway</title>'});
      });
      await submit();
      await page.locator('#confirm-error').waitFor({state:'visible'});
      assert.match(await page.locator('#confirm-error').innerText(),/交易結果待確認/);
      assert.equal(await page.locator('#send-password').inputValue(),'');
      assert.equal(await page.locator('#confirm-send-button').textContent(),'重試原交易');
      assert.equal(await page.locator('#cancel-send').textContent(),'關閉');
      assert.match(await page.locator('#confirmation-fee-hint').textContent(),/查詢不會送出交易/);
      assert.equal(await page.locator('#check-send-history').getAttribute('class'),'submit-button');
      assert.equal(sends.length,1);
      if (failure==='html502') {
        failHistory = true;
        await page.locator('#check-send-history').click();
        await page.waitForFunction(()=>!document.querySelector('#check-send-history').disabled);
        assert.match(await page.locator('#confirm-error').innerText(),/暫時無法查詢原交易/);
        failHistory = false;
        // Absence from history is also not permission to create a new payment.
        const accepted = history; history = [];
        await page.locator('#check-send-history').click();
        await page.waitForFunction(()=>!document.querySelector('#check-send-history').disabled);
        assert.match(await page.locator('#confirm-error').innerText(),/尚未找到原交易/);
        history = accepted;
      }
      if (failure==='disconnect') {
        // A retry after the preview expiry must still reach the backend with the same quote ID.
        await page.evaluate(()=>{const now=Date.now();Date.now=()=>now+3600000;});
        await submit();
        assert.equal(sends.length,2);
        assert.equal(sends[0],sends[1]);
      } else {
        await page.locator('#check-send-history').click();
        assert.equal(sends.length,1,'checking history must not send again');
      }
      await page.locator('#send-confirmation').waitFor({state:'hidden'});
      assert.ok((await page.locator('#send-feedback').innerText()).includes(hash));
      await page.unroute('**/api/wallet/send');
    }
    history=[];sends=[];
    await preview();
    for (const [status,message] of [[401,'密碼錯誤'],[500,'本機錢包儲存失敗，請檢查資料磁碟與權限']]) {
      await page.route('**/api/wallet/send',route=>route.fulfill({status,json:{code:'send_rejected',error:message}}));
      await submit();
      assert.equal(await page.locator('#confirm-error').textContent(),message);
      assert.equal(await page.locator('#check-send-history').isVisible(),false,'definite rejection keeps the normal correction flow');
      await page.unroute('**/api/wallet/send');
    }
    assert.deepEqual(errors,[]);
    console.log('PASS: HTML/empty/malformed/null responses and disconnect retain unknown outcome; history recovery never resends, expired retry preserves quote ID, definite rejection stays actionable.');
  } finally { await context.close(); }
};
