// Runs against the browser suite's real SOL/TRON pages and isolated local fixtures.
const assert = require('node:assert/strict');

module.exports = async function recoveryRegression(page) {
  const base = new URL(page.url()).origin;
  const previousLocale = await page.evaluate(() => window.FlowI18n.locale);
  const previousViewport = page.viewportSize();
  const view = name => page.locator('#app-sidebar a[href="#' + name + '"]').click();
  try {
    await page.setViewportSize({width: 1280, height: 900});
    for (const f of [
      {name: 'solana', id: 'sol', to: 'CghaUzuHcKaSKQG3UtoZzMafEPmnRk1V6LUN2Hdp8XWa', signature: '2'.repeat(88), state: 'broadcast_unknown', label: '廣播結果待確認'},
      {name: 'tron', id: 'tron', to: 'TJRabPrwbZy45sbavfcjinPJC18kjpRTv8', signature: '2'.repeat(64), state: 'expired_unconfirmed', label: '已過期，固化鏈未查到收據'}
    ]) {
      let sends = 0, rejectSend = true, failHistory = false;
      const tx = {signature: f.signature, state: f.state, to: f.to, amount: '0.000001', symbol: f.id === 'sol' ? 'SOL' : 'TRX', finalized: false};
      const historyPattern = '**/' + f.name + '/api/history';
      const sendPattern = '**/' + f.name + '/api/send';
      const history = route => route.fulfill({status: failHistory ? 503 : 200, json: failHistory ? {error: 'fixture history unavailable'} : sends ? [tx] : []});
      const send = route => {
        if (rejectSend) return route.fulfill({status: 400, json: {error: 'fixture bad password'}});
        sends++;
        failHistory = true;
        return route.fulfill({json: tx});
      };
      await page.route(historyPattern, history);
      await page.route(sendPattern, send);
      try {
        const q = id => page.locator('#' + f.id + '-' + id);
        await page.goto(base + '/' + f.name + '/#send-panel');
        await page.locator('#language-select').selectOption('zh-TW');
        await q('dashboard').waitFor({state: 'visible'});
        await page.waitForFunction(id => !document.getElementById(id + '-refresh').disabled, f.id);
        assert.equal(await q('send-result').isVisible(), false);
        await q('to').fill(f.to); await q('amount').fill('0.000001');
        await q('transfer').locator('button[type=submit]').click();
        await q('confirm').waitFor({state: 'visible'});
        await q('sign-password').fill('wrong-fixture-password');
        await q('sign').locator('button[type=submit]').click();
        await page.waitForFunction(id => document.getElementById(id + '-sign-error').textContent === 'fixture bad password', f.id);
        assert.equal(await q('sign-password').inputValue(), '');
        assert.equal(await q('send-result').isVisible(), false);
        rejectSend = false;
        await q('sign-password').fill('fixture-password-only');
        await q('sign').locator('button[type=submit]').click();
        await q('confirm').waitFor({state: 'hidden'});
        await page.waitForFunction(id => !document.getElementById(id + '-refresh').disabled && document.getElementById(id + '-error').textContent.includes('fixture history unavailable'), f.id);
        assert.equal(await q('send-result').isVisible(), true);
        assert.ok((await q('send-result').textContent()).includes(f.label));
        assert.equal(await q('send-result').locator('a').textContent(), f.signature);
        assert.ok((await q('send-result').locator('a').getAttribute('href')).includes(f.signature));
        assert.equal(await q('sign-password').inputValue(), '');
        assert.equal(sends, 1);
        await view('overview'); await q('refresh').click();
        await page.waitForFunction(id => !document.getElementById(id + '-refresh').disabled, f.id);
        assert.equal(await q('send-result').isVisible(), true);
        assert.equal(await q('send-result').locator('a').textContent(), f.signature);
        assert.equal(sends, 1, 'refresh must not resend');
        failHistory = false; await q('refresh').click();
        await page.waitForFunction(({id, signature}) => document.getElementById(id + '-history').textContent.includes(signature), f);
        assert.equal(await q('send-result').locator('a').textContent(), f.signature);
        assert.equal(sends, 1, 'history recovery must not resend');
        await page.locator('#language-select').selectOption('en');
        await page.waitForFunction(id => document.getElementById(id + '-send-result').textContent.includes('Submission status'), f.id);
        assert.equal(await q('send-result').locator('a').textContent(), f.signature);
        await page.locator('#language-select').selectOption('zh-CN');
        await page.waitForFunction(id => document.getElementById(id + '-send-result').textContent.includes('提交时状态'), f.id);
        await page.setViewportSize({width: 390, height: 844});
        assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'send result fits mobile');
        await page.setViewportSize({width: 1280, height: 900});
      } finally {
        await page.unroute(historyPattern, history);
        await page.unroute(sendPattern, send);
      }
    }
    await page.goto(base + '/tron/#send-panel');
    await page.locator('#language-select').selectOption('zh-TW');
    await page.evaluate(() => {
      window.tokenRequests = [];
      window.tokenFetch = window.fetch;
      window.fetch = (url, options) => String(url).includes('/tron/api/token?')
        ? new Promise(resolve => window.tokenRequests.push({url: String(url), resolve}))
        : window.tokenFetch(url, options);
    });
    const info = page.locator('#tron-token-info');
    const a = 'TUEZSdKsoDHQMeZwihtdoBiN46zxhGWYdH', b = 'TJRabPrwbZy45sbavfcjinPJC18kjpRTv8';
    const query = async contract => {
      await page.locator('#tron-contract').fill(contract);
      await page.locator('#tron-token-query').click();
      return page.evaluate(() => window.tokenRequests.length - 1);
    };
    const respond = async (index, data, ok = true) => page.evaluate(async ({index, data, ok}) => {
      window.tokenRequests[index].resolve({ok, json: async () => data});
      await new Promise(resolve => setTimeout(resolve, 0));
    }, {index, data, ok});
    const token = (contract, symbol) => ({contract, symbol, balance: '123', decimals: 6});
    try {
      let first = await query(a);
      await respond(first, token(a, 'INITIAL_A')); assert.match(await info.textContent(), /INITIAL_A/);
      await page.locator('#tron-contract').fill(b); assert.equal(await info.textContent(), '');
      first = await query(a); let second = await query(b);
      await respond(second, token(b, 'CURRENT_B')); await respond(first, token(a, 'STALE_A'));
      assert.match(await info.textContent(), /CURRENT_B/);
      first = await query(a); await page.locator('#tron-contract').fill(b); second = await query(a);
      await respond(first, token(a, 'OLD_A_GENERATION')); assert.equal(await info.textContent(), '');
      assert.equal(await page.locator('#tron-token-query').isDisabled(), true, 'old finally cannot unlock the new request');
      await respond(second, token(a, 'NEW_A_GENERATION')); assert.match(await info.textContent(), /NEW_A_GENERATION/);
      first = await query(a); second = await query(b);
      await respond(second, token(b, 'B_AFTER_ERROR')); await respond(first, {error: 'old token failure'}, false);
      assert.match(await info.textContent(), /B_AFTER_ERROR/);
      first = await query(b); await respond(first, {error: 'current token failure'}, false);
      assert.equal(await info.textContent(), 'current token failure');
      assert.equal(await page.locator('#tron-token-query').isEnabled(), true);
      first = await query(a); await page.locator('#tron-self').click();
      await respond(first, token(a, 'OLD_NATIVE')); assert.equal(await info.textContent(), '');
      assert.equal(await page.locator('#tron-contract').inputValue(), '');
    } finally {
      await page.evaluate(() => { window.fetch = window.tokenFetch; delete window.tokenFetch; delete window.tokenRequests; });
    }
  } finally {
    await page.setViewportSize(previousViewport);
    await page.evaluate(locale => window.FlowI18n.setLocale(locale), previousLocale);
  }
  console.log('PASS: SOL/TRON submission evidence survives history failures without resending; TRON token results reject stale success/error and A-B-A responses.');
};
