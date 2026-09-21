// Runs against the existing browser suite's real template and local API fixtures.
const assert = require('node:assert/strict');

module.exports = async function workspaceRegression(page) {
  const owner = '0x' + '1'.repeat(40), otherOwner = '0x' + '2'.repeat(40);
  const a = '0x' + 'a'.repeat(64), b = '0x' + 'b'.repeat(64);
  const previousLocale = await page.evaluate(() => window.FlowI18n.locale);
  await page.evaluate(() => { window.FlowI18n.setLocale('zh-TW'); location.hash = 'watch-panel'; });
  await page.locator('#watch-panel').waitFor({state: 'visible'});
  await page.locator('#watch-address').fill(owner);
  await page.evaluate(() => {
    window.workspaceRequests = [];
    window.workspaceFetch = window.fetch;
    window.fetch = (url, options) => String(url).includes('/api/watch/activity?')
      ? new Promise(resolve => window.workspaceRequests.push({url: String(url), resolve}))
      : window.workspaceFetch(url, options);
  });
  const result = page.locator('#watch-transactions');
  const tx = hash => ({hash, state: 'succeeded', movements: [], blockTime: null});
  const requestIndex = () => page.evaluate(() => window.workspaceRequests.length - 1);
  const submit = async hash => {
    await page.locator('#watch-hash').fill(hash);
    await page.locator('#watch-tx-form button').click();
    return requestIndex();
  };
  const block = async () => {
    await page.locator('#watch-history-form button').click();
    return requestIndex();
  };
  const respond = async (index, data, ok = true) => {
    await page.evaluate(async ({index, data, ok}) => {
      window.workspaceRequests[index].resolve({ok, json: async () => data});
      // Drain the request/json continuations and the translation observer.
      await new Promise(resolve => setTimeout(resolve, 0));
    }, {index, data, ok});
  };
  const shows = async hash => assert.ok((await result.locator('a').getAttribute('href')).endsWith(hash));
  try {
    let first = await submit(a), second = await submit(b);
    await respond(second, tx(b)); await shows(b);
    await respond(first, tx(a)); await shows(b);
    assert.equal(await page.locator('#watch-hash').inputValue(), b);

    second = await submit(b); await respond(second, tx(b)); await shows(b); // Single-query recovery.
    first = await submit(a); second = await submit(b);
    await respond(second, tx(b)); await respond(first, {error: 'old receipt failure'}, false); await shows(b);

    first = await submit(a); let history = await block();
    await respond(history, {block: 123, coverage: 'fixture', hashes: [b]});
    await respond(first, tx(a)); assert.match(await result.textContent(), /123/);

    history = await block(); second = await submit(b);
    await respond(second, tx(b)); await respond(history, {block: 456, coverage: 'fixture', hashes: [a]}); await shows(b);
    assert.equal(await page.locator('#watch-history-form button').isDisabled(), false);

    history = await block(); await respond(history, {block: 456, coverage: 'fixture', hashes: [a]});
    await result.locator('button').click(); first = await requestIndex();
    assert.equal(await page.locator('#watch-hash').inputValue(), a, 'block hash selection updates the input');
    second = await submit(b); await respond(second, tx(b)); await respond(first, tx(a)); await shows(b);

    first = await submit(a); second = await submit(b);
    await respond(first, tx(a)); assert.equal(await result.locator('a').count(), 0, 'old result cannot replace the newer loading state');
    await respond(second, tx(b)); await shows(b);

    history = await block(); second = await submit(b);
    await respond(second, tx(b)); await respond(history, {error: 'old block failure'}, false); await shows(b);
    assert.equal(await page.locator('#watch-history-form button').isDisabled(), false);

    for (const [field, value] of [['watch-hash', b], ['watch-block', '999'], ['watch-address', otherOwner]]) {
      first = await submit(a);
      await page.locator('#' + field).fill(value);
      assert.equal(await result.textContent(), '', field + ' clears the displayed result');
      await respond(first, tx(a));
      assert.equal(await result.textContent(), '', field + ' invalidates the pending response');
    }
    history = await block(); await page.locator('#watch-block').fill('1000');
    await respond(history, {error: 'invalidated block failure'}, false);
    assert.equal(await result.textContent(), '');

    second = await submit(b); await respond(second, {error: 'latest receipt failure'}, false);
    assert.equal(await result.textContent(), 'latest receipt failure', 'current errors remain visible');
    history = await block(); await respond(history, {error: 'latest block failure'}, false);
    assert.equal(await result.textContent(), 'latest block failure');
    history = await block(); await respond(history, {block: 789, coverage: 'fixture', hashes: []});
    assert.match(await result.textContent(), /789/); assert.match(await result.textContent(), /此區塊沒有找到相關交易/);

    second = await submit(b);
    await respond(second, {...tx(b), movements: [{kind: 'receive', asset: 'ETH', raw: '7', evidence: 'fixture transfer'}]});
    await shows(b); assert.match(await result.textContent(), /fixture transfer/);
    await page.locator('#watch-address').fill(owner);
    assert.equal(await result.textContent(), '', 'editing an address clears an already completed result');

    await page.locator('#watch-save').click();
    await page.locator('#watch-address').fill(otherOwner);
    first = await submit(a);
    await page.locator('#watch-saved button').filter({hasText: owner}).click();
    await respond(first, tx(a));
    assert.equal(await result.textContent(), '', 'saved-address selection also invalidates pending receipts');
    assert.equal(await page.locator('#watch-address').inputValue(), owner);
    await page.locator('#watch-saved .contact-row').filter({hasText: owner}).getByRole('button', {name: '移除', exact: true}).click();
    await page.waitForFunction(() => !document.querySelector('#watch-form button[type=submit]').disabled);
    await page.evaluate(() => {
      window.workspaceAssetRequests = [];
      window.fetch = (url, options) => /\/api\/(balance\?|watch\/token\?)/.test(String(url))
        ? new Promise(resolve => window.workspaceAssetRequests.push({url: String(url), resolve}))
        : window.workspaceFetch(url, options);
    });
    const assets = page.locator('#watch-result'), assetButton = page.locator('#watch-form button[type=submit]');
    const tokenA = '0x' + '3'.repeat(40), tokenB = '0x' + '4'.repeat(40);
    const assetCount = () => page.evaluate(() => window.workspaceAssetRequests.length);
    const submitAssets = async () => { await assetButton.click(); return (await assetCount()) - 1; };
    const balance = address => ({address, eth: '1', block: '100', checkedAt: '2026-09-22T00:00:00Z'});
    const token = contract => ({contract, symbol: 'TEST', balance: '2'});
    const respondAsset = async (index, data, ok = true) => {
      await page.evaluate(async ({index, data, ok}) => {
        window.workspaceAssetRequests[index].resolve({ok, json: async () => data});
        await new Promise(resolve => setTimeout(resolve, 0));
      }, {index, data, ok});
    };
    for (const mode of ['success', 'error', 'A-B-A']) {
      await page.locator('#watch-address').fill(owner);
      await page.locator('#watch-contract').fill(tokenA);
      first = await submitAssets(); const count = await assetCount();
      await page.locator('#watch-address').fill(otherOwner);
      if (mode === 'A-B-A') await page.locator('#watch-address').fill(owner);
      assert.equal(await assets.textContent(), '', 'address editing clears the asset loading/result state');
      assert.equal(await assetButton.isDisabled(), true, 'invalidating input does not unlock an in-flight query');
      await respondAsset(first, mode === 'error' ? {error: 'old balance failure'} : balance(owner), mode !== 'error');
      assert.equal(await assets.textContent(), '', 'invalidated balance successes/errors remain discarded, including A-B-A');
      assert.equal(await assetCount(), count, 'invalidated balance must not launch the old token query');
      assert.equal(await assetButton.isDisabled(), false);
    }
    for (const mode of ['success', 'error', 'A-B-A']) {
      await page.locator('#watch-address').fill(owner);
      await page.locator('#watch-contract').fill(tokenA);
      first = await submitAssets(); await respondAsset(first, balance(owner));
      const tokenRequest = (await assetCount()) - 1;
      assert.ok(await page.evaluate(index => window.workspaceAssetRequests[index].url.includes('/api/watch/token?'), tokenRequest));
      await page.locator('#watch-contract').fill(tokenB);
      if (mode === 'A-B-A') await page.locator('#watch-contract').fill(tokenA);
      assert.equal(await assets.textContent(), '', 'contract editing clears the asset loading/result state');
      assert.equal(await assetButton.isDisabled(), true);
      await respondAsset(tokenRequest, mode === 'error' ? {error: 'old token failure'} : token(tokenA), mode !== 'error');
      assert.equal(await assets.textContent(), '', 'invalidated token successes/errors remain discarded, including A-B-A');
      assert.equal(await assetButton.isDisabled(), false);
    }
    await page.locator('#watch-address').fill(otherOwner);
    await page.locator('#watch-contract').fill(tokenB);
    first = await submitAssets(); await respondAsset(first, balance(otherOwner));
    await respondAsset((await assetCount()) - 1, token(tokenB));
    assert.ok((await assets.textContent()).includes(otherOwner) && (await assets.textContent()).includes(tokenB), 'normal latest balance and token results recover');
    await page.locator('#watch-contract').fill(tokenA);
    assert.equal(await assets.textContent(), '', 'editing a contract clears a completed result');
    first = await submitAssets(); await respondAsset(first, balance(otherOwner));
    await respondAsset((await assetCount()) - 1, {error: 'latest token failure'}, false);
    assert.match(await assets.textContent(), /latest token failure/);
    await page.locator('#watch-address').fill(owner);
    assert.equal(await assets.textContent(), '', 'editing an address clears a completed result');
    await page.locator('#watch-contract').fill('');
    first = await submitAssets(); await respondAsset(first, {error: 'latest balance failure'}, false);
    assert.equal(await assets.textContent(), 'latest balance failure', 'current asset errors remain visible');
    await page.locator('#watch-save').click();
    await page.locator('#watch-address').fill(otherOwner);
    first = await submitAssets(); const count = await assetCount();
    await page.locator('#watch-saved button').filter({hasText: owner}).click();
    assert.equal(await page.locator('#watch-address').inputValue(), owner);
    assert.equal(await assets.textContent(), '', 'saved-address selection clears the asset result');
    assert.equal(await assetButton.isDisabled(), true);
    assert.equal(await assetCount(), count, 'saved selection cannot start a second query before the first settles');
    await respondAsset(first, balance(otherOwner));
    assert.equal(await assets.textContent(), '', 'saved-address selection invalidates the pending balance');
    assert.equal(await assetButton.isDisabled(), false);
    await page.locator('#watch-saved button').filter({hasText: owner}).click();
    await respondAsset((await assetCount()) - 1, balance(owner));
    assert.ok((await assets.textContent()).includes(owner), 'saved-address lookup recovers after stale work settles');
    await page.locator('#watch-saved .contact-row').filter({hasText: owner}).getByRole('button', {name: '移除', exact: true}).click();
    console.log('PASS: workspace transaction/block and asset query generations, stale successes/errors, A-B-A, saved addresses, completed-result invalidation and recovery (mock APIs).');
  } finally {
    await page.evaluate(locale => {
      window.fetch = window.workspaceFetch;
      delete window.workspaceFetch;
      delete window.workspaceRequests;
      delete window.workspaceAssetRequests;
      window.FlowI18n.setLocale(locale);
    }, previousLocale);
  }
};
