// Exercise the real wallet interval with local API fixtures and a controlled browser clock.
const assert = require('node:assert/strict');
const { setTimeout: delay } = require('node:timers/promises');

module.exports = async function escrowPolling(existingPage) {
  const base = new URL(existingPage.url()).origin;
  const context = await existingPage.context().browser().newContext();
  await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
  const page = await context.newPage(), errors = [];
  page.on('pageerror', error => errors.push(String(error)));
  const contract = '0xabcdefabcdefabcdefabcdefabcdefabcdefabcd';
  const tx = {action:'approve', to:contract.toUpperCase(), amount:'1', symbol:'USDC', hash:'0x'+'a'.repeat(64), state:'pending', createdAt:new Date().toISOString()};
  let history = [], allowance = '0', historyReads = 0, escrowReads = 0;
  let escrowEnabled = true, escrowFailures = 0;
  await page.route('**/api/wallet/history', route => { historyReads++; return route.fulfill({json:{transactions:history}}); });
  await page.route('**/api/wallet/escrow', route => {
    escrowReads++;
    if (escrowFailures > 0) {
      escrowFailures--;
      return route.fulfill({status:503,json:{error:'fixture escrow RPC unavailable'}});
    }
    return route.fulfill({json:{enabled:escrowEnabled,contract:escrowEnabled?contract:'',token:'0x'+'2'.repeat(40),balance:'10',allowance}});
  });
  async function until(check) {
    for (let attempt = 0; attempt < 200; attempt++) {
      if (await check()) return;
      await delay(10);
    }
    assert.fail('wallet polling fixture did not settle');
  }
  async function settle(before) {
    await until(() => historyReads > before);
    await until(() => page.locator('#refresh-wallet').isEnabled());
  }
  async function seed(item) {
    history = [item];
    const before = historyReads;
    await page.locator('#escrow-refresh').evaluate(button => button.click());
    await settle(before);
  }
  async function tick(expectRefresh) {
    const before = historyReads, beforeEscrow = escrowReads;
    await page.clock.runFor(10000);
    if (expectRefresh) {
      await settle(before);
      assert.equal(historyReads, before + 1, 'one wallet refresh per interval');
      assert.equal(escrowReads, beforeEscrow + 1, 'poll also updates escrow balance and allowance');
    } else {
      await delay(50);
      assert.equal(historyReads, before, 'unrelated or completed transactions do not keep polling');
      assert.equal(escrowReads, beforeEscrow);
    }
  }
  try {
    await page.clock.install();
    await page.goto(base + '/#vault-panel');
    await page.waitForFunction(() => !document.getElementById('refresh-wallet').disabled && !document.getElementById('vault-preview').disabled);
    await page.locator('#contract-tab-escrow').click();
    await page.locator('#escrow-enabled').waitFor({state:'visible'});
    await page.waitForFunction(() => !document.getElementById('escrow-refresh').disabled);
    await page.clock.pauseAt(await page.evaluate(() => Date.now() + 1000));

    for (const state of ['submitted','pending','broadcast_unknown','receipt_unavailable','reorg_detected']) {
      await seed({...tx,state});
      await tick(true);
    }
    // A transient escrow RPC error must hide stale operations without losing the polling target.
    await seed(tx);
    escrowFailures = 1;
    await tick(true);
    assert.equal(await page.locator('#escrow-enabled').isVisible(), false);
    assert.match(await page.locator('#escrow-error').textContent(), /fixture escrow RPC unavailable/);
    // The next interval must recover and display the later receipt/allowance, then stop polling.
    history = [{...tx,state:'succeeded'}]; allowance = '1';
    await tick(true);
    assert.equal(await page.locator('#escrow-enabled').isVisible(), true);
    assert.match(await page.locator('#escrow-history').textContent(), /鏈上執行成功/);
    assert.match(await page.locator('#escrow-balance').textContent(), /已授權：1 USDC/);
    await tick(false);
    await seed({...tx,state:'reverted'}); await tick(false);
    await seed({...tx,to:'0x'+'9'.repeat(40)}); await tick(false);
    await seed(tx);
    escrowEnabled = false;
    await tick(true);
    assert.equal(await page.locator('#escrow-enabled').isVisible(), false);
    await tick(false);
    escrowEnabled = true;
    for (const action of ['vault_deposit','escrow_fund']) {
      await seed({...tx,action}); await tick(true);
    }
    assert.deepEqual(errors, []);
    console.log('PASS: escrow approval polling survives transient RPC errors, updates receipts/allowance, stops on completion or explicit disable, ignores unrelated spenders, and preserves vault/escrow polling.');
  } finally { await context.close(); }
};
