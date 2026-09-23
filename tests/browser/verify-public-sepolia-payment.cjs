// Read-only recheck of the recorded public Sepolia browser payment.
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');

const evidencePath = path.resolve(__dirname, '../../docs/evidence/payment-sepolia-2026-09-23/e2e.json');
const evidence = JSON.parse(fs.readFileSync(evidencePath, 'utf8'));
const base = 'https://wallet.tedlin.fyi';
const accountPath = '/accounts/bed966601653c8aebaab79b4a1dfa089/';
const rpcURL = 'https://ethereum-sepolia-rpc.publicnode.com';
const same = (a, b) => a?.toLowerCase() === b?.toLowerCase();

async function rpc(method, params) {
  const response = await fetch(rpcURL, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
    signal: AbortSignal.timeout(15000),
  });
  assert.equal(response.status, 200, `${method} HTTP`);
  const body = await response.json();
  assert.ok(!body.error, `${method}: ${JSON.stringify(body.error)}`);
  return body.result;
}

(async () => {
  assert.match(evidence.hash, /^0x[0-9a-f]{64}$/i);
  assert.equal(evidence.status, 'finalized');
  assert.equal(await rpc('eth_chainId', []), '0xaa36a7');
  const tx = await rpc('eth_getTransactionByHash', [evidence.hash]);
  const receipt = await rpc('eth_getTransactionReceipt', [evidence.hash]);
  assert.ok(tx && receipt);
  assert.ok(same(tx.from, evidence.from) && same(tx.to, evidence.to));
  assert.equal(BigInt(tx.value), 1000000000000n);
  assert.equal(BigInt(tx.chainId), 11155111n);
  assert.equal(BigInt(tx.nonce), BigInt(evidence.quoteNonce));
  assert.equal(tx.input, '0x');
  assert.equal(receipt.status, '0x1');
  assert.equal(BigInt(receipt.blockNumber), BigInt(evidence.blockNumber));
  assert.ok(same(receipt.blockHash, evidence.blockHash));
  const block = await rpc('eth_getBlockByNumber', [receipt.blockNumber, false]);
  assert.ok(same(block.hash, receipt.blockHash));
  const prior = '0x' + (BigInt(receipt.blockNumber) - 1n).toString(16);
  const amount = BigInt(tx.value);
  const fee = BigInt(receipt.gasUsed) * BigInt(receipt.effectiveGasPrice);
  assert.equal(BigInt(evidence.senderBeforeBlockWei) - BigInt(evidence.senderAfterBlockWei), amount + fee);
  assert.equal(BigInt(evidence.recipientAfterBlockWei) - BigInt(evidence.recipientBeforeBlockWei), amount);
  let balanceReads = 0;
  for (const [address, blockTag, recorded] of [
    [evidence.from, prior, evidence.senderBeforeBlockWei],
    [evidence.from, receipt.blockNumber, evidence.senderAfterBlockWei],
    [evidence.to, prior, evidence.recipientBeforeBlockWei],
    [evidence.to, receipt.blockNumber, evidence.recipientAfterBlockWei],
  ]) {
    try {
      assert.equal(BigInt(await rpc('eth_getBalance', [address, blockTag])).toString(), recorded);
      balanceReads++;
    } catch (error) {
      if (!/historical state .* is not available/.test(String(error))) throw error;
    }
  }
  const finalized = await rpc('eth_getBlockByNumber', ['finalized', false]);
  assert.ok(BigInt(finalized.number) >= BigInt(receipt.blockNumber));

  const response = await fetch(base + accountPath + 'api/wallet/history');
  assert.equal(response.status, 200);
  const history = await response.json();
  assert.ok(!history.refreshError);
  const item = history.transactions.find(row => same(row.hash, evidence.hash));
  assert.equal(item?.state, 'succeeded');
  assert.equal(item.finalized, true);
  assert.equal(item.action, 'eth');
  assert.ok(same(item.to, evidence.to));
  assert.equal(item.amount, evidence.amountETH);

  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(String(error)));
    await page.goto(base + accountPath + '#history-panel');
    await page.locator('#wallet-dashboard').waitFor({ state: 'visible' });
    await page.waitForFunction(hash => [...document.querySelectorAll('#wallet-history a')].some(link => link.href.toLowerCase().endsWith('/tx/' + hash.toLowerCase())), evidence.hash);
    const row = page.locator('#wallet-history .history-row').filter({ has: page.locator(`a[href$="/tx/${evidence.hash}"]`) });
    assert.match(await row.innerText(), /鏈上執行成功/);
    assert.match(await row.innerText(), /已達鏈上終局性/);
    assert.deepEqual(errors, []);
  } finally {
    await browser.close();
  }
  console.log(`PASS: public Sepolia finalized receipt, recorded balance arithmetic, API and browser history ${evidence.hash}; historical balances re-read: ${balanceReads}/4`);
})().catch(error => { console.error(error); process.exitCode = 1; });
