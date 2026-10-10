// Fails if any console view shows a production identifier.
//
// The screenshots come from a demo instance that only holds fabricated data, so
// this renders every view and checks the visible text against the real
// operator's domains, tenant ids and key prefixes. Run against the demo port
// while it is up.
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

const BASE = 'http://127.0.0.1:4799';
const PASSWORD = process.argv[2];

const FORBIDDEN = [
  'zzzgfwacnz1',
  'office.mzz.edu.rs',
  'office.bo.edu.kg',
  'LNBuuAsbrS47XUM',
  'onmicrosoft.com',
  'm365_05b9f7a',
  'm365_9b7a656',
  'm365_9d64558',
  'm365_6eb3851',
  'm365_924b307',
  'm365_7734f1a',
  'm365_9720c89',
  'm365_a94257a',
  'm365_c1f4417',
  'm365_fd95b9e',
  'm365_cfd4ce0',
  'm365_05deea2',
  'm365_406c730',
  'm365_8ef0257',
  'm365_5ae8f0b',
];

const VIEWS = [0, 1, 2, 3, 4, 5, 6];

(async () => {
  const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe';
  const browser = await chromium.launch({
    executablePath: fs.existsSync(CHROME) ? CHROME : undefined,
  });
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await context.addInitScript(() => {
    try { sessionStorage.setItem('m365_admin_session', '1'); } catch {}
  });
  const page = await context.newPage();

  const res = await page.request.post(`${BASE}/api/admin/login`, { data: { password: PASSWORD } });
  if (!res.ok()) { console.log('login failed', res.status()); process.exit(1); }

  await page.goto(`${BASE}/`, { waitUntil: 'networkidle' });
  await new Promise((r) => setTimeout(r, 1500));

  let bad = 0;
  for (const i of VIEWS) {
    await page.locator('button').nth(i).click();
    await new Promise((r) => setTimeout(r, 1000));
    const text = await page.evaluate(() => document.body.innerText);
    for (const needle of FORBIDDEN) {
      if (text.includes(needle)) { console.log(`LEAK view ${i}: ${needle}`); bad++; }
    }
  }

  console.log(bad === 0 ? 'clean: no production identifiers in any view' : `found ${bad} leaks`);
  await browser.close();
  process.exit(bad === 0 ? 0 : 1);
})().catch((e) => { console.error(e.message); process.exit(1); });