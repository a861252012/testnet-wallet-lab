'use strict';
const assert = require('node:assert/strict');

module.exports = async function walletRefresh(existingPage) {
  const base = new URL(existingPage.url()).origin;
  const context = await existingPage.context().browser().newContext({locale:'zh-TW'});
  await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
  const page = await context.newPage(), errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const contract = '0xabcdefabcdefabcdefabcdefabcdefabcdefabcd';
  let vaultBalance = '1', escrowBalance = '5', historyReads = 0;
  let tokenReads = 0, activeTokens = 0, maxActiveTokens = 0, gate, escrowGate;
  let tokenBalance = '2';
  const releases = [];
  function holdToken(fail) {
    let release, entered;
    const wait = new Promise(resolve => { release = resolve; });
    const started = new Promise(resolve => { entered = resolve; });
    gate = {wait, entered, fail};
    releases.push(release);
    return {started, release};
  }
  function holdEscrow() {
    let release, entered;
    const wait = new Promise(resolve => { release = resolve; });
    const started = new Promise(resolve => { entered = resolve; });
    escrowGate = {wait, entered};
    releases.push(release);
    return {started, release};
  }
  await page.route('**/api/wallet/history', route => {
    historyReads++;
    return route.fulfill({json:{transactions:[],canCreateTransaction:true}});
  });
  await page.route('**/api/wallet/vault', route => route.fulfill({json:{enabled:true,contract,balance:vaultBalance,balanceRaw:String(BigInt(vaultBalance)*10n**18n)}}));
  await page.route('**/api/wallet/escrow', async route => {
    const balance = escrowBalance, held = escrowGate;
    escrowGate = undefined;
    if (held) { held.entered(); await held.wait; }
    return route.fulfill({json:{enabled:true,contract,token:'0x'+'2'.repeat(40),balance,allowance:'10'}});
  });
  await page.route('**/api/wallet/token', async route => {
    tokenReads++;
    activeTokens++;
    maxActiveTokens = Math.max(maxActiveTokens, activeTokens);
    const held = gate, balance = tokenBalance;
    gate = undefined;
    try {
      if (held) { held.entered(); await held.wait; }
      if (held?.fail) return await route.fulfill({status:503,json:{error:'fixture token unavailable'}});
      const address = route.request().postDataJSON().contract;
      await route.fulfill({json:{contract:address,symbol:'TST',decimals:6,balance,balanceRaw:String(BigInt(balance)*1000000n),allowanceRaw:'0',trustedMetadata:true}});
    } finally { activeTokens--; }
  });
  async function tokensSettled() {
    await page.waitForFunction(() => document.querySelector('#token-list').getAttribute('aria-busy') === 'false');
  }
  try {
    await page.goto(base + '/#vault-panel');
    await page.waitForFunction(() => document.querySelector('#wallet-loading').hidden && !document.querySelector('#refresh-wallet').disabled && !document.querySelector('#vault-preview').disabled);
    await tokensSettled();

    const beforeTokens = tokenReads, beforeHistory = historyReads;
    const slow = holdToken(false);
    vaultBalance = '2';
    await page.locator('#vault-refresh').click();
    await slow.started;
    await page.waitForFunction(() => document.querySelector('#vault-deposited-balance').textContent === '2 ETH' && !document.querySelector('#refresh-wallet').disabled, null, {timeout:3000});
    assert.equal(historyReads, beforeHistory + 1);
    assert.equal(await page.locator('#token-list').getAttribute('aria-busy'), 'true');
    assert.equal(tokenReads, beforeTokens + 1);

    // A second refresh queues one later token pass, without overlapping the held request.
    vaultBalance = '3';
    await page.locator('#vault-refresh').click();
    await page.waitForFunction(() => document.querySelector('#vault-deposited-balance').textContent === '3 ETH' && !document.querySelector('#refresh-wallet').disabled);
    assert.equal(tokenReads, beforeTokens + 1);
    slow.release();
    await tokensSettled();
    assert.equal(tokenReads, beforeTokens + 4, 'two sequential tokens and one coalesced follow-up pass');

    await page.locator('#contract-tab-escrow').click();
    await page.waitForFunction(() => !document.querySelector('#escrow-refresh').disabled);
    const failure = holdToken(true);
    escrowBalance = '7';
    await page.locator('#escrow-refresh').click();
    await failure.started;
    await page.waitForFunction(() => document.querySelector('#escrow-balance').textContent.includes('7 USDC') && !document.querySelector('#refresh-wallet').disabled, null, {timeout:3000});
    failure.release();
    await tokensSettled();
    assert.equal(await page.locator('#token-error').evaluate(element => element.hidden), false, 'token error remains recorded while its panel is inactive');
    assert.equal(await page.locator('#escrow-error').isHidden(), true);
    assert.equal(await page.locator('#escrow-refresh').isEnabled(), true);

    await page.locator('#escrow-refresh').click();
    await page.waitForFunction(() => !document.querySelector('#refresh-wallet').disabled);
    await tokensSettled();
    assert.equal(await page.locator('#token-error').evaluate(element => element.hidden), true);

    // A send can finish while a prior refresh is waiting on a contract response.
    const buyer = '0x'+'1'.repeat(40), seller = '0x'+'9'.repeat(40), orderId = 'refresh-after-send';
    let sends = 0;
    await page.route('**/api/wallet/escrow/order?*', route => route.fulfill({json:{buyer,seller,orderId,state:sends?'funded':'none',amount:'1',amountRaw:'1000000',finalized:false}}));
    await page.route('**/api/wallet/quote', route => route.fulfill({json:{
      id:'refresh-payment',action:'escrow_fund',from:buyer,to:seller,contract,symbol:'USDC',amount:'1',amountRaw:'1000000',
      escrow:{action:'escrow_fund',orderId,buyer,seller,token:'0x'+'2'.repeat(40)},
      maxFeeEth:'0.00001',totalEth:'0.00001',expiresAt:new Date(Date.now()+120000).toISOString(),nonce:'0',gasLimit:'100000',maxFeePerGas:'2',maxPriorityFeePerGas:'1',data:'0x12345678',
    }}));
    await page.route('**/api/wallet/send', route => {
      sends++;
      escrowBalance = '6'; tokenBalance = '1';
      return route.fulfill({json:{hash:'0x'+'a'.repeat(64),action:'escrow_fund',state:'submitted'}});
    });
    await page.locator('#escrow-new').evaluate(element => { element.open = true; });
    await page.locator('#escrow-reference').fill(orderId);
    await page.locator('#escrow-seller').fill(seller);
    await page.locator('#escrow-amount').fill('1');
    await page.locator('#escrow-fund-preview').click();
    await page.locator('#send-confirmation').waitFor({state:'visible'});
    const oldToken = holdToken(false), oldEscrow = holdEscrow(), beforeSendRefresh = historyReads;
    // Polling can invoke this same refresh while a confirmation remains open.
    await page.locator('#escrow-refresh').evaluate(button => button.click());
    await Promise.all([oldToken.started, oldEscrow.started]);
    await page.locator('#send-password').fill('isolated-fixture-password');
    await page.locator('#confirm-send-button').click();
    await page.locator('#send-confirmation').waitFor({state:'hidden'});
    oldToken.release();
    await tokensSettled();
    oldEscrow.release();
    await page.waitForFunction(() => !document.querySelector('#refresh-wallet').disabled && document.querySelector('#escrow-balance').textContent.includes('6 USDC'), null, {timeout:3000});
    await tokensSettled();
    assert.equal(historyReads, beforeSendRefresh + 2, 'send completion queues a fresh wallet snapshot while the previous refresh is busy');
    assert.deepEqual(await page.locator('#token-list .token-balance').allTextContents(), ['1','1']);
    assert.equal(sends, 1, 'queued refresh never resends the payment');
    assert.equal(maxActiveTokens, 1);
    assert.deepEqual(errors, []);
    console.log('PASS: slow token reads do not delay vault/escrow status or controls; token passes coalesce, stay sequential, and recover independently.');
  } finally {
    releases.forEach(release => release());
    await context.unrouteAll({behavior:'ignoreErrors'});
    await context.close();
  }
};
