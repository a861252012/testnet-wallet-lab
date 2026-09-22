'use strict';
const assert = require('node:assert/strict');

module.exports = async function escrowSpeedup(existingPage) {
  const base = new URL(existingPage.url()).origin;
  const context = await existingPage.context().browser().newContext({locale:'zh-TW'});
  await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
  const page = await context.newPage(), errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const buyer = '0x'+'1'.repeat(40), seller = '0x'+'2'.repeat(40);
  const contract = '0x'+'3'.repeat(40), token = '0x'+'4'.repeat(40);
  const originalHash = '0x'+'a'.repeat(64), winnerHash = '0x'+'b'.repeat(64);
  let original, history, preview, sent = 0, lookups = [];
  await page.route('**/api/wallet/history', route => route.fulfill({json:{transactions:history,canCreateTransaction:false}}));
  await page.route('**/api/wallet/escrow', route => route.fulfill({json:{enabled:true,contract,token,balance:'10',allowance:'5'}}));
  await page.route('**/api/wallet/escrow/order?*', route => {
    const query = new URL(route.request().url()).searchParams;
    lookups.push({buyer:query.get('buyer'),orderId:query.get('orderId')});
    return route.fulfill({json:{buyer,seller,orderId:query.get('orderId'),state:'funded',amount:'5',amountRaw:'5000000',finalized:true}});
  });
  await page.route('**/api/wallet/quote', route => {
    const request = route.request().postDataJSON();
    assert.equal(request.hash, originalHash);
    assert.ok(['speedup','cancel'].includes(request.action));
    const cancel = request.action === 'cancel';
    preview = {id:'fixture-replacement',action:request.action,from:buyer,to:cancel?buyer:original.to,contract:cancel?'':contract,amount:cancel?'0':'5',amountRaw:cancel?'0':'5000000',symbol:cancel?'ETH':'USDC',maxFeeEth:'0.00001',totalEth:'0.00001',expiresAt:new Date(Date.now()+120000).toISOString(),nonce:'0',gasLimit:'100000',maxFeePerGas:'2',maxPriorityFeePerGas:'1',data:cancel?'0x':'0x12345678',method:request.action};
    if (!cancel) preview.escrow = {action:original.escrowAction,orderId:original.orderId,buyer,seller,token};
    return route.fulfill({json:preview});
  });
  await page.route('**/api/wallet/send', route => {
    assert.equal(route.request().postDataJSON().quoteId, preview.id);
    assert.equal(preview.action, 'speedup');
    sent++;
    const winner = {...original,action:'speedup',hash:winnerHash,state:'succeeded'};
    history = [winner,{...original,state:'replaced',replacedBy:winnerHash}];
    return route.fulfill({json:winner});
  });
  try {
    for (const [action,label,destination] of [
      ['escrow_fund','付款至合約','目前錢包 → 託管合約'],
      ['escrow_release','放款給收款人','託管合約 → 收款人'],
      ['escrow_refund','退款給付款人','託管合約 → 原付款人'],
    ]) {
      for (const orderId of ['order-'+action,'0x'+'c'.repeat(64)]) {
        original = {action,escrowAction:action,orderId,escrowBuyer:buyer,escrowContract:contract,to:action==='escrow_refund'?buyer:seller,amount:'5',symbol:'USDC',hash:originalHash,state:'pending',createdAt:new Date().toISOString()};
        history = [original];
        lookups = [];
        await page.goto(base + '/#history-panel');
        await page.waitForFunction(() => document.querySelector('#wallet-loading').hidden && !document.querySelector('#refresh-wallet').disabled);
        await page.evaluate(() => sessionStorage.clear());
        await page.locator('#wallet-history').getByRole('button',{name:'取消交易',exact:true}).click();
        await page.locator('#send-confirmation').waitFor({state:'visible'});
        assert.doesNotMatch(await page.locator('#quote-details').textContent(), /訂單編號|託管合約|原操作/);
        await page.locator('#cancel-send').click();

        await page.locator('#wallet-history').getByRole('button',{name:'加速交易',exact:true}).click();
        await page.locator('#send-confirmation').waitFor({state:'visible'});
        assert.equal(await page.locator('#confirm-title').textContent(), '確認加速原交易');
        const details = await page.locator('#quote-details').textContent();
        for (const expected of [orderId,'原操作'+label,'託管合約'+contract,'代幣合約'+token,destination]) assert.ok(details.includes(expected), expected);
        for (const locale of ['en','zh-CN','zh-TW']) {
          await page.evaluate(locale => window.FlowI18n.setLocale(locale), locale);
          if (locale === 'en') assert.doesNotMatch(await page.locator('#send-confirmation').innerText(), /[\u3400-\u9fff]/);
        }
        await page.locator('#send-password').fill('isolated-fixture-password');
        await page.locator('#confirm-send-button').click();
        await page.locator('#send-confirmation').waitFor({state:'hidden'});
        await page.waitForFunction(() => !document.querySelector('#refresh-wallet').disabled);

        await page.goto(base + '/#vault-panel');
        await page.waitForFunction(() => document.querySelector('#wallet-loading').hidden && !document.querySelector('#refresh-wallet').disabled);
        await page.locator('#contract-tab-escrow').click();
        await page.waitForFunction(() => !document.querySelector('#escrow-refresh').disabled);
        const winner = page.locator('#escrow-history > p').filter({has:page.locator(`a[href$="${winnerHash}"]`)});
        assert.equal(await winner.count(), 1, 'winning speedup remains in the payment list after reload');
        assert.ok((await winner.textContent()).includes(label));
        await winner.getByRole('button',{name:'查看訂單',exact:true}).click();
        await page.locator('#escrow-order').waitFor({state:'visible'});
        assert.deepEqual(lookups.at(-1), {buyer,orderId});
      }
    }
    assert.equal(sent, 6);
    assert.deepEqual(errors, []);
    console.log('PASS: escrow fund/release/refund speedups retain summaries, destinations, translations and order lookup after reload; cancel carries no escrow data.');
  } finally { await context.close(); }
};
