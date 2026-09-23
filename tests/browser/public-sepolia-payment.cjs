// Opt-in public Sepolia acceptance. This sends exactly one real transaction.
// Keep the wallet password in a 0600 file outside the repository.
'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { setTimeout: delay } = require('node:timers/promises');
const { chromium } = require('playwright');

const base = 'https://wallet.tedlin.fyi';
const rpcURL = 'https://ethereum-sepolia-rpc.publicnode.com';
const accountID = 'bed966601653c8aebaab79b4a1dfa089';
const from = '0x11A0130EecDF4648efed6a0E3A8901Bf9E44B293';
const to = '0xe7A597C22c77E983B353CB39fb53a5479ec8c474';
const amount = '0.000001';
const value = 1000000000000n;
const expectedRevision = process.env.PUBLIC_PAYMENT_REVISION;
const passwordFile = process.env.PUBLIC_PAYMENT_PASSWORD_FILE;
const evidenceFile = process.env.PUBLIC_PAYMENT_EVIDENCE_FILE;

assert.equal(process.env.PUBLIC_PAYMENT_SEND, 'YES', 'Set PUBLIC_PAYMENT_SEND=YES to authorize one Sepolia payment');
assert.match(expectedRevision || '', /^[0-9a-f]{40}$/);
assert.ok(passwordFile && evidenceFile);
assert.equal(fs.statSync(passwordFile).mode & 0o077, 0, 'password file must be owner-only');
const evidenceFD = fs.openSync(evidenceFile, 'wx', 0o600);
let evidence = { revision: expectedRevision, chainId: 11155111, from, to, amountETH: amount, status: 'preflight' };
function save(patch) {
  evidence = { ...evidence, ...patch };
  fs.ftruncateSync(evidenceFD, 0);
  fs.writeSync(evidenceFD, JSON.stringify(evidence, null, 2) + '\n', 0, 'utf8');
  fs.fsyncSync(evidenceFD);
}
save({ startedAt: new Date().toISOString() });

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
const hex = n => '0x' + n.toString(16);
const same = (a, b) => a?.toLowerCase() === b?.toLowerCase();

(async () => {
  const health = await fetch(base + '/healthz');
  assert.equal(health.status, 200);
  assert.equal(health.headers.get('x-app-version'), expectedRevision);
  assert.equal(await rpc('eth_chainId', []), '0xaa36a7');
  const wallet = await (await fetch(`${base}/accounts/${accountID}/api/wallet`)).json();
  assert.equal(wallet.chainId, 11155111);
  assert.ok(same(wallet.address, from));
  const accountPath = `/accounts/${accountID}`;
  const historyBefore = await (await fetch(base + accountPath + '/api/wallet/history')).json();
  assert.ok(!historyBefore.refreshError);
  assert.ok(historyBefore.transactions.every(tx => tx.state === 'succeeded' || tx.state === 'reverted' || tx.state === 'replaced'));
  const nonceBefore = await rpc('eth_getTransactionCount', [from, 'pending']);
  const senderBefore = await rpc('eth_getBalance', [from, 'latest']);
  const recipientBefore = await rpc('eth_getBalance', [to, 'latest']);
  assert.ok(BigInt(senderBefore) > value);
  save({ status: 'ready', nonceBefore, senderBeforeWei: BigInt(senderBefore).toString(), recipientBeforeWei: BigInt(recipientBefore).toString(), historyCountBefore: historyBefore.transactions.length });

  const browser = await chromium.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    const page = await context.newPage();
    const pageErrors = [];
    page.on('pageerror', error => pageErrors.push(String(error)));
    await page.goto(base + accountPath + '/#send-panel');
    await page.locator('#wallet-dashboard').waitFor({ state: 'visible' });
    await page.waitForFunction(() => document.body.dataset.view === 'send-panel');
    await page.locator('#send-asset').selectOption('eth');
    await page.locator('#send-to').fill(to);
    await page.locator('#send-amount').fill(amount);
    const [quoteResponse] = await Promise.all([
      page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/api/wallet/quote') && r.request().method() === 'POST'),
      page.locator('#send-form button[type="submit"]').click(),
    ]);
    assert.equal(quoteResponse.status(), 200);
    const quote = await quoteResponse.json();
    assert.equal(quote.action, 'eth');
    assert.ok(same(quote.from, from) && same(quote.to, to));
    assert.equal(quote.amountRaw, value.toString());
    assert.equal(BigInt(quote.nonce), BigInt(nonceBefore));
    await page.locator('#send-confirmation').waitFor({ state: 'visible' });
    const details = await page.locator('#quote-details').textContent();
    assert.ok(details.includes(from) && details.includes(to) && details.includes(amount));
    save({ status: 'quoted', quoteNonce: quote.nonce, quoteGasLimit: quote.gasLimit, quoteMaxFeePerGas: quote.maxFeePerGas });

    const password = fs.readFileSync(passwordFile, 'utf8').trimEnd();
    assert.ok(password.length >= 12);
    await page.locator('#send-password').fill(password);
    const [sendResponse] = await Promise.all([
      page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/api/wallet/send') && r.request().method() === 'POST', { timeout: 30000 }),
      page.locator('#confirm-send-button').click(),
    ]);
    const sent = await sendResponse.json();
    save({ status: 'submitted', sendHTTP: sendResponse.status(), hash: sent.hash || null, sendState: sent.state || null });
    assert.equal(sendResponse.status(), 200, 'send may be uncertain; inspect journal before any retry');
    assert.match(sent.hash, /^0x[0-9a-fA-F]{64}$/);
    assert.equal(sent.action, 'eth');
    assert.ok(same(sent.to, to));
    assert.equal(sent.amount, amount);
    assert.equal(sent.reused, undefined);
    console.log(`SUBMITTED ${sent.hash}`);

    let receipt;
    for (let i = 0; i < 30; i++) {
      receipt = await rpc('eth_getTransactionReceipt', [sent.hash]);
      if (receipt) break;
      await delay(12000);
    }
    assert.ok(receipt, 'receipt unavailable; do not submit another payment');
    const tx = await rpc('eth_getTransactionByHash', [sent.hash]);
    assert.ok(tx && same(tx.from, from) && same(tx.to, to));
    assert.equal(BigInt(tx.value), value);
    assert.equal(BigInt(tx.chainId), 11155111n);
    assert.equal(BigInt(tx.nonce), BigInt(quote.nonce));
    assert.equal(tx.input, '0x');
    assert.equal(receipt.status, '0x1');
    const block = await rpc('eth_getBlockByNumber', [receipt.blockNumber, false]);
    assert.ok(same(block.hash, receipt.blockHash));
    const at = BigInt(receipt.blockNumber);
    const beforeBlock = hex(at - 1n);
    const senderAtBefore = BigInt(await rpc('eth_getBalance', [from, beforeBlock]));
    const senderAtAfter = BigInt(await rpc('eth_getBalance', [from, receipt.blockNumber]));
    const recipientAtBefore = BigInt(await rpc('eth_getBalance', [to, beforeBlock]));
    const recipientAtAfter = BigInt(await rpc('eth_getBalance', [to, receipt.blockNumber]));
    const fee = BigInt(receipt.gasUsed) * BigInt(receipt.effectiveGasPrice);
    assert.equal(senderAtBefore - senderAtAfter, value + fee);
    assert.equal(recipientAtAfter - recipientAtBefore, value);
    save({ status: 'mined', blockNumber: at.toString(), blockHash: receipt.blockHash, receiptStatus: receipt.status, gasUsed: BigInt(receipt.gasUsed).toString(), effectiveGasPriceWei: BigInt(receipt.effectiveGasPrice).toString(), feeWei: fee.toString(), senderBeforeBlockWei: senderAtBefore.toString(), senderAfterBlockWei: senderAtAfter.toString(), recipientBeforeBlockWei: recipientAtBefore.toString(), recipientAfterBlockWei: recipientAtAfter.toString() });
    console.log(`MINED ${sent.hash} block ${at}`);

    let siteTx;
    for (let i = 0; i < 20; i++) {
      const response = await fetch(base + accountPath + '/api/wallet/history');
      assert.equal(response.status, 200);
      const history = await response.json();
      assert.ok(!history.refreshError);
      siteTx = history.transactions.find(item => same(item.hash, sent.hash));
      if (siteTx?.state === 'succeeded') break;
      await delay(3000);
    }
    assert.equal(siteTx?.state, 'succeeded');
    assert.equal(siteTx.action, 'eth');
    assert.ok(same(siteTx.to, to));
    assert.equal(siteTx.amount, amount);
    await page.goto(base + accountPath + '/#history-panel');
    await page.waitForFunction(hash => [...document.querySelectorAll('#wallet-history a')].some(link => link.href.toLowerCase().endsWith('/tx/' + hash.toLowerCase())), sent.hash);
    assert.deepEqual(pageErrors, []);
    save({ status: 'site-verified', siteState: siteTx.state, siteFinalized: siteTx.finalized, browserHistoryVisible: true, pageErrors });
    console.log(`SITE ${sent.hash} succeeded`);

    let finalized;
    for (let i = 0; i < 110; i++) {
      finalized = await rpc('eth_getBlockByNumber', ['finalized', false]);
      if (BigInt(finalized.number) >= at) break;
      await delay(12000);
    }
    assert.ok(BigInt(finalized.number) >= at, 'not finalized within observation window');
    assert.ok(same((await rpc('eth_getBlockByNumber', [receipt.blockNumber, false])).hash, receipt.blockHash));
    const finalHistory = await (await fetch(base + accountPath + '/api/wallet/history')).json();
    assert.ok(!finalHistory.refreshError);
    assert.equal(finalHistory.transactions.find(item => same(item.hash, sent.hash))?.finalized, true);
    save({ status: 'finalized', finalizedBlock: BigInt(finalized.number).toString(), siteFinalized: true, checkedAt: new Date().toISOString() });
    console.log(`FINALIZED ${sent.hash} at ${finalized.number}`);
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); save({ status: 'needs-investigation', error: String(error) }); process.exitCode = 1; }).finally(() => fs.closeSync(evidenceFD));
